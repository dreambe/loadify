// Package script implements an embedded JavaScript (goja) load driver. A run
// that carries a ScriptBundle is executed by running the user's script once per
// iteration. The script defines an `iteration` (or `default`) function and uses
// the injected `http` API to make requests; each call's timing and status are
// folded into a single per-iteration Result for the metrics pipeline.
//
// Each virtual user gets its own goja runtime (goja runtimes are not safe for
// concurrent use), all compiled from the same program, so VUs are isolated.
package script

import (
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mrand "math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
	"github.com/dop251/goja"
)

// entrypoints are the global function names tried, in order, as the per-
// iteration body.
var entrypoints = []string{"iteration", "default"}

// dataKey is the reserved ScriptBundle module under which a test's data-feeder
// rows (a JSON array of objects) are carried to the worker.
const dataKey = "__data__"

// Driver runs a JS scenario under load.
type Driver struct {
	prog       *goja.Program
	group      string
	client     *http.Client
	timeout    time.Duration
	iterBudget time.Duration // per-iteration wall-clock budget; goja Interrupt on overrun
	dataset    []map[string]any

	mu  sync.Mutex
	vus map[int]*vu
}

// New compiles the bundle into a script Driver. proto only influences the
// default group label; the script itself decides what traffic to generate.
func New(bundle *loadifyv1.ScriptBundle, p *plan.Plan, _ loadifyv1.Protocol) (protocols.Driver, error) {
	if bundle == nil || strings.TrimSpace(bundle.MainJs) == "" {
		return nil, fmt.Errorf("script: empty bundle")
	}
	prog, err := goja.Compile("main.js", bundle.MainJs, true)
	if err != nil {
		return nil, fmt.Errorf("script: compile: %w", err)
	}
	group := "script"
	if p != nil && p.HTTP != nil && p.HTTP.Group != "" {
		group = p.HTTP.Group
	}
	d := &Driver{prog: prog, group: group, iterBudget: plan.DefaultScriptTimeout}
	if p != nil {
		d.iterBudget = p.ScriptTimeout()
	}
	if raw := bundle.Modules[dataKey]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &d.dataset); err != nil {
			return nil, fmt.Errorf("script: invalid data feeder JSON: %w", err)
		}
	}
	return d, nil
}

// Prepare builds the shared HTTP client used by every VU's http binding.
func (d *Driver) Prepare(_ context.Context) error {
	d.timeout = 30 * time.Second
	tr := &http.Transport{
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 1024,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
		// TLS is verified by default; scripts target real endpoints.
		TLSClientConfig: &tls.Config{},
	}
	d.client = &http.Client{Transport: tr, Timeout: d.timeout}
	d.vus = make(map[int]*vu)
	return nil
}

// Exec runs one scenario iteration for the VU.
func (d *Driver) Exec(ctx context.Context, vuState *protocols.VU) protocols.Result {
	v, err := d.vuFor(vuState.ID)
	if err != nil {
		return protocols.Result{Group: d.group, ErrorKind: "script_init"}
	}

	v.acc.reset()
	v.acc.ctx = ctx

	callErr := d.callBounded(v)

	res := v.acc.result(d.group)
	applyCallErr(&res, callErr)
	return res
}

// applyCallErr marks a result failed from a call error. A timeout (goja
// interrupt) always wins over any partial error kind already recorded.
func applyCallErr(res *protocols.Result, callErr error) {
	if callErr == nil {
		return
	}
	res.OK = false
	if _, timeout := callErr.(*goja.InterruptedError); timeout {
		res.ErrorKind = "script_timeout"
		return
	}
	if res.ErrorKind == "" {
		res.ErrorKind = "script_error"
	}
}

// callBounded runs the iteration entrypoint under a wall-clock budget: if it
// overruns (e.g. an infinite loop in user JS), goja is interrupted so the VU is
// freed instead of pinning a core. The interrupt surfaces as a call error.
func (d *Driver) callBounded(v *vu) error {
	if d.iterBudget <= 0 {
		_, err := v.fn(goja.Undefined())
		return err
	}
	timer := time.AfterFunc(d.iterBudget, func() { v.rt.Interrupt("script timeout") })
	defer timer.Stop()
	_, err := v.fn(goja.Undefined())
	v.rt.ClearInterrupt()
	return err
}

// ExecMulti runs one iteration and returns every result it produced. A scenario
// harness pushes one result per step (plus a transaction total) via __emit__;
// a plain script yields the single folded result. This is how per-interface
// and transaction metrics are captured for scenarios.
func (d *Driver) ExecMulti(ctx context.Context, vuState *protocols.VU) []protocols.Result {
	v, err := d.vuFor(vuState.ID)
	if err != nil {
		return []protocols.Result{{Group: d.group, ErrorKind: "script_init"}}
	}
	v.acc.reset()
	v.acc.ctx = ctx
	v.em.results = v.em.results[:0]

	callErr := d.callBounded(v)

	if len(v.em.results) > 0 {
		// Scenario emitted labeled per-step results; copy out (the backing slice
		// is reused next iteration).
		out := make([]protocols.Result, len(v.em.results))
		copy(out, v.em.results)
		return out
	}
	res := v.acc.result(d.group)
	applyCallErr(&res, callErr)
	return []protocols.Result{res}
}

// Teardown releases idle connections.
func (d *Driver) Teardown(_ context.Context) error {
	if d.client != nil {
		d.client.CloseIdleConnections()
	}
	return nil
}

// vuFor lazily builds the goja runtime for a VU id.
func (d *Driver) vuFor(id int) (*vu, error) {
	d.mu.Lock()
	v := d.vus[id]
	d.mu.Unlock()
	if v != nil {
		return v, nil
	}
	v, err := d.buildVU()
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.vus[id] = v
	d.mu.Unlock()
	return v, nil
}

type vu struct {
	rt  *goja.Runtime
	fn  goja.Callable
	acc *accumulator
	em  *emitter
}

func (d *Driver) buildVU() (*vu, error) {
	rt := goja.New()
	rt.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	acc := &accumulator{}
	em := &emitter{}

	bindConsole(rt)
	bindSleep(rt)
	bindCheck(rt, acc)
	bindFuncs(rt)
	bindEmit(rt, em)
	d.bindData(rt)
	if err := bindHTTP(rt, d.client, acc); err != nil {
		return nil, err
	}

	if _, err := rt.RunProgram(d.prog); err != nil {
		return nil, fmt.Errorf("script: run: %w", err)
	}
	fn, err := resolveEntrypoint(rt)
	if err != nil {
		return nil, err
	}
	return &vu{rt: rt, fn: fn, acc: acc, em: em}, nil
}

// emitter collects per-step results pushed from a scenario's compiled harness
// via __emit__, so one iteration can yield several labeled metrics (per
// interface + a transaction total). Owned by one single-threaded VU runtime.
type emitter struct {
	results []protocols.Result
}

// bindData exposes the data feeder to a VU runtime: `data` is the full row
// array and `nextRow()` returns rows sequentially per VU (cycling), so each
// virtual user walks the dataset independently.
func (d *Driver) bindData(rt *goja.Runtime) {
	if len(d.dataset) == 0 {
		return
	}
	_ = rt.Set("data", rt.ToValue(d.dataset))
	idx := 0
	_ = rt.Set("nextRow", func(goja.FunctionCall) goja.Value {
		row := d.dataset[idx%len(d.dataset)]
		idx++
		return rt.ToValue(row)
	})
}

func resolveEntrypoint(rt *goja.Runtime) (goja.Callable, error) {
	for _, name := range entrypoints {
		if fn, ok := goja.AssertFunction(rt.Get(name)); ok {
			return fn, nil
		}
	}
	return nil, fmt.Errorf("script: no %v function defined", entrypoints)
}

// accumulator folds the http calls made during one iteration into a single
// Result. It is owned by one VU runtime (single-threaded), so it needs no lock.
type accumulator struct {
	ctx        context.Context
	latencyUs  int64
	ttfbUs     int64
	sent       int64
	recv       int64
	status     int32
	calls      int
	checks     int
	checkFails int
	failed     bool
	errKind    string
	method     string
	url        string
	respBody   string
	errInfo    bool // method/url/respBody describe a failed call
}

func (a *accumulator) reset() {
	a.latencyUs, a.ttfbUs, a.sent, a.recv = 0, 0, 0, 0
	a.status, a.calls, a.checks, a.checkFails = 0, 0, 0, 0
	a.failed, a.errKind = false, ""
	a.method, a.url, a.respBody, a.errInfo = "", "", "", false
}

// noteCall records one http call's request/response snippet for the live log:
// the first call wins, unless a later call fails (errors are more interesting).
func (a *accumulator) noteCall(method, url, body string, failed bool) {
	if a.url != "" && (a.errInfo || !failed) {
		return
	}
	if len(body) > protocols.RespBodyCap {
		body = body[:protocols.RespBodyCap]
	}
	a.method, a.url, a.respBody, a.errInfo = method, url, body, failed
}

func (a *accumulator) result(group string) protocols.Result {
	r := protocols.Result{
		Group:     group,
		Method:    a.method,
		URL:       a.url,
		Status:    a.status,
		LatencyUs: a.latencyUs,
		TTFBUs:    a.ttfbUs,
		SentBytes: a.sent,
		RecvBytes: a.recv,
		ErrorKind: a.errKind,
		RespBody:  a.respBody,
	}
	// OK when at least one call was made and none failed.
	r.OK = a.calls > 0 && !a.failed
	if a.calls == 0 && a.errKind == "" {
		r.ErrorKind = "no_request"
	}
	return r
}

// bindConsole exposes console.log / console.error (no-op sinks that keep
// scripts from crashing on logging).
func bindConsole(rt *goja.Runtime) {
	obj := rt.NewObject()
	noop := func(goja.FunctionCall) goja.Value { return goja.Undefined() }
	_ = obj.Set("log", noop)
	_ = obj.Set("error", noop)
	_ = obj.Set("warn", noop)
	_ = rt.Set("console", obj)
}

// bindCheck exposes check(name, condition): an assertion. A failed check marks
// the iteration as failed (counted as an error and surfaced in the live log),
// mirroring k6's check(). Returns the boolean condition.
func bindCheck(rt *goja.Runtime, acc *accumulator) {
	_ = rt.Set("check", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		cond := call.Argument(1).ToBoolean()
		acc.checks++
		if !cond {
			acc.checkFails++
			acc.failed = true
			if acc.errKind == "" {
				acc.errKind = "check:" + name
			}
		}
		return rt.ToValue(cond)
	})
}

// bindSleep exposes sleep(seconds) backed by a real pause.
func bindSleep(rt *goja.Runtime) {
	_ = rt.Set("sleep", func(call goja.FunctionCall) goja.Value {
		secs := call.Argument(0).ToFloat()
		if secs > 0 {
			time.Sleep(time.Duration(secs * float64(time.Second)))
		}
		return goja.Undefined()
	})
}

// bindFuncs injects data-generation helpers for parameterization:
//
//	uuid()            → RFC4122-ish random v4 string
//	randomInt(a,b)    → integer in [a,b]
//	random()          → float in [0,1)
//	timestamp()       → unix milliseconds
//	now()             → RFC3339 timestamp
//	counter()         → per-VU monotonically increasing integer (from 1)
//	randomItem(array) → a random element of the array
func bindFuncs(rt *goja.Runtime) {
	var ctr int64
	_ = rt.Set("uuid", func(goja.FunctionCall) goja.Value { return rt.ToValue(newUUID()) })
	_ = rt.Set("randomInt", func(call goja.FunctionCall) goja.Value {
		a := call.Argument(0).ToInteger()
		b := call.Argument(1).ToInteger()
		if b < a {
			a, b = b, a
		}
		return rt.ToValue(a + mrand.Int63n(b-a+1))
	})
	_ = rt.Set("random", func(goja.FunctionCall) goja.Value { return rt.ToValue(mrand.Float64()) })
	_ = rt.Set("timestamp", func(goja.FunctionCall) goja.Value { return rt.ToValue(time.Now().UnixMilli()) })
	_ = rt.Set("now", func(goja.FunctionCall) goja.Value { return rt.ToValue(time.Now().Format(time.RFC3339)) })
	_ = rt.Set("counter", func(goja.FunctionCall) goja.Value {
		ctr++
		return rt.ToValue(ctr)
	})
	_ = rt.Set("randomItem", func(call goja.FunctionCall) goja.Value {
		arr := call.Argument(0).Export()
		if s, ok := arr.([]any); ok && len(s) > 0 {
			return rt.ToValue(s[mrand.Intn(len(s))])
		}
		return goja.Undefined()
	})
}

// bindEmit exposes __emit__(result) used by the compiled scenario harness to
// record one labeled step (or transaction) result per call.
func bindEmit(rt *goja.Runtime, em *emitter) {
	_ = rt.Set("__emit__", func(call goja.FunctionCall) goja.Value {
		o := call.Argument(0).ToObject(rt)
		if o == nil {
			return goja.Undefined()
		}
		get := func(k string) goja.Value { return o.Get(k) }
		str := func(k string) string {
			if v := get(k); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
				return v.String()
			}
			return ""
		}
		i64 := func(k string) int64 {
			if v := get(k); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
				return v.ToInteger()
			}
			return 0
		}
		boolv := func(k string) bool {
			if v := get(k); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
				return v.ToBoolean()
			}
			return false
		}
		body := str("resp_body")
		if len(body) > protocols.RespBodyCap {
			body = body[:protocols.RespBodyCap]
		}
		em.results = append(em.results, protocols.Result{
			Group:     str("group"),
			Method:    str("method"),
			URL:       str("url"),
			Status:    int32(i64("status")),
			OK:        boolv("ok"),
			ErrorKind: str("error_kind"),
			LatencyUs: i64("latency_us"),
			TTFBUs:    i64("ttfb_us"),
			SentBytes: i64("sent_bytes"),
			RecvBytes: i64("recv_bytes"),
			RespBody:  body,
		})
		return goja.Undefined()
	})
}

// newUUID builds a random v4 UUID string without external dependencies.
func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
