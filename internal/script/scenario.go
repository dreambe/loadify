package script

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

// CompileScenario turns a no-code scenario into a goja script the existing
// script driver runs. The scenario spec is embedded as a JSON literal and a
// fixed harness interprets it: {{var}} interpolation, JSON field extraction
// for chaining, per-step assertions via check(), and weighted step selection.
//
// Keeping the harness fixed (data-driven) rather than code-generating each
// step keeps the emitted script small and the escaping trivially safe — only
// the embedded JSON needs care, and json.Marshal handles that.
func CompileScenario(sc *plan.ScenarioConfig) (string, error) {
	if sc == nil || len(sc.Steps) == 0 {
		return "", fmt.Errorf("script: empty scenario")
	}
	js, err := embedSpec(sc)
	if err != nil {
		return "", err
	}
	return "var __SCENARIO__ = " + js + ";\n" + scenarioHarness, nil
}

// embedSpec marshals a scenario to a JS-safe literal. U+2028/U+2029 are valid in
// JSON but terminate JS string literals, so escape them.
func embedSpec(sc *plan.ScenarioConfig) (string, error) {
	spec, err := json.Marshal(sc)
	if err != nil {
		return "", err
	}
	return strings.NewReplacer("\u2028", "\\u2028", "\u2029", "\\u2029").Replace(string(spec)), nil
}

// scenarioCommon holds the helpers and per-step runner shared by the load and
// setup harnesses. __VU_VARS__ persists across a VU's iterations — it carries
// per-VU setup extracts into every iteration, and is the bag a global-setup run
// reads back to fold values into the run env.
const scenarioCommon = `
var __VU_VARS__ = {};
function _get(o, path) {
  var segs = String(path).split(".");
  for (var i = 0; i < segs.length; i++) {
    if (o === null || o === undefined) return undefined;
    o = o[segs[i]];
  }
  return o;
}
function _tmplFunc(token) {
  // Built-in generators usable directly in {{...}}: uuid, timestamp, now,
  // random, randomInt(a,b). Returns undefined when token is not a function.
  var m = token.match(/^(\w+)\s*\(([^)]*)\)$/);
  var name = m ? m[1] : token;
  var args = m && m[2].trim() ? m[2].split(",").map(function (x) { return parseFloat(x); }) : [];
  switch (name) {
    case "uuid": return uuid();
    case "timestamp": return timestamp();
    case "now": return now();
    case "random": return random();
    case "randomInt": return randomInt(args[0] || 0, args[1] || 0);
  }
  return undefined;
}
function _interp(s, vars) {
  if (s === undefined || s === null) return s;
  return String(s).replace(/\{\{\s*([\w.]+(?:\([^)]*\))?)\s*\}\}/g, function (m, k) {
    var v = vars[k];
    if (v === undefined || v === null) {
      var f = _tmplFunc(k);
      if (f !== undefined) return String(f);
      return "";
    }
    return typeof v === "object" ? JSON.stringify(v) : String(v);
  });
}
function _looseEq(actual, want) {
  if (typeof actual === "number") return actual === parseFloat(want);
  if (typeof actual === "boolean") return String(actual) === String(want).trim();
  if (actual === null) return String(want).trim() === "null";
  return String(actual) === String(want);
}
function _assertOne(a, r) {
  var actual;
  if (a.source === "status") actual = r.status;
  else if (a.source === "body") actual = r.body;
  else {
    var p;
    try { p = JSON.parse(r.body); } catch (e) { return false; }
    actual = _get(p, a.path);
  }
  if (a.op === "exists") return actual !== undefined && actual !== null;
  if (actual === undefined) return false;
  var got = typeof actual === "object" ? JSON.stringify(actual) : String(actual);
  switch (a.op) {
    case "contains": return got.indexOf(a.value) >= 0;
    case "eq": return _looseEq(actual, a.value);
    case "ne": return !_looseEq(actual, a.value);
    case "gt": return parseFloat(actual) > parseFloat(a.value);
    case "lt": return parseFloat(actual) < parseFloat(a.value);
    case "gte": return parseFloat(actual) >= parseFloat(a.value);
    case "lte": return parseFloat(actual) <= parseFloat(a.value);
  }
  return false;
}
function _runStep(step, vars, idx) {
  var headers = {};
  if (step.headers) for (var k in step.headers) headers[k] = _interp(step.headers[k], vars);
  var url = _interp(step.url, vars);
  if (step.params && step.params.length) {
    // Interpolate each value, THEN URL-encode, so {{var}} resolves before
    // escaping (encoding first would turn "{{" into "%7B%7B" and break it).
    var _qs = [];
    for (var _pi = 0; _pi < step.params.length; _pi++) {
      var _p = step.params[_pi];
      if (!_p.key) continue;
      _qs.push(encodeURIComponent(_p.key) + "=" + encodeURIComponent(_interp(_p.value, vars)));
    }
    if (_qs.length) url += (url.indexOf("?") >= 0 ? "&" : "?") + _qs.join("&");
  }
  var body = step.body ? _interp(step.body, vars) : "";
  var r = http.request(step.method || "GET", url, body, { headers: headers });
  var label = step.name || ("step" + (idx + 1));
  var ok = true, reason = "";
  if (step.asserts && step.asserts.length) {
    for (var i = 0; i < step.asserts.length; i++) {
      var a = step.asserts[i];
      var pass = _assertOne(a, r);
      if (!pass && ok) { ok = false; reason = "assert " + a.source + " " + a.op + " " + (a.value || ""); }
    }
  } else {
    ok = r.status < 400 && !r.error;
    if (!ok) reason = r.error ? r.error : "status " + r.status;
  }
  // Extraction runs before emit so a broken chain surfaces as a step failure
  // instead of silently feeding empty {{vars}} into later steps: an extract is
  // configured but the body is missing/unparsable (extract_failed) or a path
  // resolves to nothing (extract_missing).
  if (step.extracts && step.extracts.length) {
    var parsed = null, parseOK = false;
    if (r.body) { try { parsed = JSON.parse(r.body); parseOK = true; } catch (e) {} }
    if (!parseOK && ok) { ok = false; reason = "extract_failed"; }
    for (var j = 0; j < step.extracts.length; j++) {
      var ex = step.extracts[j];
      var val = parseOK ? _get(parsed, ex.path) : undefined;
      vars[ex["var"]] = val;
      if (parseOK && val === undefined && ok) { ok = false; reason = "extract_missing"; }
    }
  }
  // Emit this step as its own labeled result (per-interface metrics + drill-down).
  __emit__({
    group: label, method: step.method || "GET", url: url,
    status: r.status, ok: ok, error_kind: ok ? "" : reason,
    latency_us: Math.round((r.duration_ms || 0) * 1000), ttfb_us: Math.round((r.duration_ms || 0) * 1000),
    sent_bytes: body.length, recv_bytes: (r.body || "").length,
    req_body: body, resp_body: r.body || ""
  });
  return { r: r, ok: ok, ms: r.duration_ms || 0 };
}
`

// scenarioHarness drives the load: per-VU setup runs once before a VU's first
// iteration, then each iteration runs the workload (each-iteration) steps.
// Setup steps (once_per_vu) and global steps (once_global, already resolved at
// launch) are excluded from the workload loop and from the transaction total.
const scenarioHarness = scenarioCommon + `
var __SETUP_DONE__ = false;
function _isOnce(s) { return s === "once_per_vu" || s === "once_global"; }
function _runSetupOnce() {
  if (__SETUP_DONE__) return;
  __SETUP_DONE__ = true;
  var steps = __SCENARIO__.steps;
  // once_per_vu steps run here, extracting into the VU-scoped bag.
  for (var i = 0; i < steps.length; i++) {
    if (steps[i].scope === "once_per_vu") _runStep(steps[i], __VU_VARS__, i);
  }
}
function iteration() {
  _runSetupOnce();
  var vars = {};
  for (var sk in __VU_VARS__) vars[sk] = __VU_VARS__[sk];
  if (typeof nextRow === "function") {
    var row = nextRow();
    if (row) for (var k in row) vars[k] = row[k];
  }
  var steps = __SCENARIO__.steps;
  if (__SCENARIO__.mode === "weighted") {
    var pool = [];
    for (var i = 0; i < steps.length; i++) if (!_isOnce(steps[i].scope)) pool.push(i);
    if (!pool.length) return;
    var total = 0;
    for (var pj = 0; pj < pool.length; pj++) total += steps[pool[pj]].weight || 1;
    var pick = Math.random() * total, acc = 0, chosen = pool[0];
    for (var pj = 0; pj < pool.length; pj++) {
      acc += steps[pool[pj]].weight || 1;
      if (pick <= acc) { chosen = pool[pj]; break; }
    }
    _runStep(steps[chosen], vars, chosen);
  } else {
    // Sequence: run the workload steps, then emit a transaction total.
    var txnMs = 0, txnOK = true, ran = 0;
    for (var i = 0; i < steps.length; i++) {
      if (_isOnce(steps[i].scope)) continue;
      var out = _runStep(steps[i], vars, i);
      txnMs += out.ms;
      if (!out.ok) txnOK = false;
      ran++;
    }
    if (ran > 1) {
      __emit__({
        group: "txn:" + (__SCENARIO__.name || "scenario"),
        ok: txnOK, error_kind: txnOK ? "" : "step_failed",
        latency_us: Math.round(txnMs * 1000), ttfb_us: Math.round(txnMs * 1000)
      });
    }
  }
}
`

// scenarioSetupHarness runs every provided step once, in order, accumulating
// extracts into __VU_VARS__. RunGlobalSetup compiles the once_global steps with
// this harness, runs a single iteration, then reads __VU_VARS__ back.
const scenarioSetupHarness = scenarioCommon + `
function iteration() {
  var steps = __SCENARIO__.steps;
  for (var i = 0; i < steps.length; i++) _runStep(steps[i], __VU_VARS__, i);
}
`

// setupVarsName is the runtime global the harness accumulates extracts into;
// RunGlobalSetup reads it back after running the setup steps.
const setupVarsName = "__VU_VARS__"

// CompileScenarioSetup builds a script that runs the given steps once, in order,
// accumulating their extracts into the variable bag.
func CompileScenarioSetup(steps []plan.ScenarioStep) (string, error) {
	if len(steps) == 0 {
		return "", fmt.Errorf("script: empty setup")
	}
	spec, err := json.Marshal(&plan.ScenarioConfig{Mode: "sequence", Steps: steps})
	if err != nil {
		return "", err
	}
	js := strings.NewReplacer(" ", "\\u2028", " ", "\\u2029").Replace(string(spec))
	return "var __SCENARIO__ = " + js + ";\n" + scenarioSetupHarness, nil
}

// GlobalSetupSteps returns the once_global steps of a scenario, in order. These
// run once at launch; their extracted values are folded into the run env.
func GlobalSetupSteps(sc *plan.ScenarioConfig) []plan.ScenarioStep {
	if sc == nil {
		return nil
	}
	var out []plan.ScenarioStep
	for _, st := range sc.Steps {
		if st.Scope == plan.ScopeOnceGlobal {
			out = append(out, st)
		}
	}
	return out
}

// RunGlobalSetup runs a scenario's once_global setup steps a single time and
// returns the variables they extracted, so a launcher can fold them into the
// run environment (where {{var}} references resolve to literals for every
// worker). A failed setup step is returned as an error so the caller can abort
// the launch rather than start a run whose chain is already broken. The variable
// bag is read straight from the runtime, so values are not subject to the live
// log's 1 KB body cap (a token can exceed it).
func RunGlobalSetup(ctx context.Context, steps []plan.ScenarioStep) (map[string]string, error) {
	if len(steps) == 0 {
		return map[string]string{}, nil
	}
	js, err := CompileScenarioSetup(steps)
	if err != nil {
		return nil, err
	}
	drv, err := New(&loadifyv1.ScriptBundle{MainJs: js}, &plan.Plan{}, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		return nil, err
	}
	d, ok := drv.(*Driver)
	if !ok {
		return nil, fmt.Errorf("script: setup driver unavailable")
	}
	if err := d.Prepare(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = d.Teardown(context.Background()) }()

	for _, res := range d.ExecMulti(ctx, &protocols.VU{ID: 1}) {
		if !res.OK {
			kind := res.ErrorKind
			if kind == "" {
				kind = "step failed"
			}
			return nil, fmt.Errorf("setup step %q: %s", res.Group, kind)
		}
	}

	d.mu.Lock()
	v := d.vus[1]
	d.mu.Unlock()
	if v == nil {
		return map[string]string{}, nil
	}
	return exportVars(v.rt.Get(setupVarsName)), nil
}

// exportVars converts the JS variable bag into string env values suitable for
// {{KEY}} substitution.
func exportVars(val goja.Value) map[string]string {
	out := map[string]string{}
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return out
	}
	m, ok := val.Export().(map[string]interface{})
	if !ok {
		return out
	}
	for k, raw := range m {
		switch t := raw.(type) {
		case nil:
			// drop nulls — a missing extract should not inject an empty literal
		case string:
			out[k] = t
		case bool:
			out[k] = strconv.FormatBool(t)
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case int64:
			out[k] = strconv.FormatInt(t, 10)
		default:
			if b, err := json.Marshal(raw); err == nil {
				out[k] = string(b)
			}
		}
	}
	return out
}
