package script_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/script"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

func TestScriptIterationTimeout(t *testing.T) {
	// A runaway loop must be interrupted within the budget and reported as a
	// timeout, not hang the VU.
	p, _ := plan.Parse([]byte(`{"protocol":"script","script_timeout_ms":150}`))
	drv, err := script.New(&loadifyv1.ScriptBundle{MainJs: `function iteration(){ while(true){} }`}, p, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := drv.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	defer drv.Teardown(ctx)

	done := make(chan protocols.Result, 1)
	go func() { done <- drv.Exec(ctx, &protocols.VU{ID: 1}) }()
	select {
	case res := <-done:
		if res.OK {
			t.Fatal("runaway iteration reported ok")
		}
		if res.ErrorKind != "script_timeout" {
			t.Errorf("error kind = %q, want script_timeout", res.ErrorKind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("iteration was not interrupted by the timeout")
	}
}

func TestScriptDriverRunsHTTP(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if r.Method == http.MethodPost {
			if r.Header.Get("X-Test") != "1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	src := `
		function iteration() {
			var r = http.get(BASE + "/a");
			if (!r.ok) { throw "get failed"; }
			http.post(BASE + "/b", "payload", { headers: { "X-Test": "1" } });
		}`
	// Inject the base URL as a global by prepending a var declaration.
	src = "var BASE = " + jsString(srv.URL) + ";\n" + src

	p, _ := plan.Parse([]byte(`{"protocol":"script"}`))
	drv, err := script.New(&loadifyv1.ScriptBundle{MainJs: src}, p, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	defer drv.Teardown(context.Background())

	res := drv.Exec(ctx, &protocols.VU{ID: 1})
	if !res.OK {
		t.Fatalf("iteration not ok: kind=%q", res.ErrorKind)
	}
	if res.RecvBytes == 0 || res.SentBytes == 0 {
		t.Errorf("expected sent and recv bytes, got sent=%d recv=%d", res.SentBytes, res.RecvBytes)
	}
	if atomic.LoadInt64(&hits) != 2 {
		t.Errorf("server hits = %d, want 2", atomic.LoadInt64(&hits))
	}
}

func TestScriptDriverThrowMarksFailure(t *testing.T) {
	src := `function iteration() { throw new Error("boom"); }`
	drv, err := script.New(&loadifyv1.ScriptBundle{MainJs: src}, nil, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := drv.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	defer drv.Teardown(ctx)
	res := drv.Exec(ctx, &protocols.VU{ID: 1})
	if res.OK {
		t.Fatal("expected failure when the script throws")
	}
}

func TestScriptCheckFailsIteration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	run := func(src string) protocols.Result {
		src = "var BASE = " + jsString(srv.URL) + ";\n" + src
		drv, err := script.New(&loadifyv1.ScriptBundle{MainJs: src}, nil, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := drv.Prepare(ctx); err != nil {
			t.Fatal(err)
		}
		defer drv.Teardown(ctx)
		return drv.Exec(ctx, &protocols.VU{ID: 1})
	}

	// A passing check leaves a successful request OK.
	if res := run(`function iteration(){ var r = http.get(BASE); check("ok", r.status === 200); }`); !res.OK {
		t.Errorf("passing check should stay OK, kind=%q", res.ErrorKind)
	}
	// A failing check fails the iteration even though the request succeeded.
	res := run(`function iteration(){ http.get(BASE); check("bad", false); }`)
	if res.OK {
		t.Error("failing check should fail the iteration")
	}
	if res.ErrorKind != "check:bad" {
		t.Errorf("error kind = %q, want check:bad", res.ErrorKind)
	}
}

func TestScriptDataFeeder(t *testing.T) {
	var got []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("X-User"))
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	src := `function iteration(){ var row = nextRow(); http.get(BASE, { headers: { "X-User": row.user } }); }`
	src = "var BASE = " + jsString(srv.URL) + ";\n" + src
	bundle := &loadifyv1.ScriptBundle{
		MainJs:  src,
		Modules: map[string]string{"__data__": `[{"user":"alice"},{"user":"bob"},{"user":"carol"}]`},
	}
	drv, err := script.New(bundle, nil, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := drv.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	defer drv.Teardown(ctx)

	vu := &protocols.VU{ID: 1}
	for i := 0; i < 4; i++ {
		if res := drv.Exec(ctx, vu); !res.OK {
			t.Fatalf("iter %d not ok: %q", i, res.ErrorKind)
		}
		vu.Iteration++
	}
	mu.Lock()
	defer mu.Unlock()
	// nextRow cycles the dataset per VU: alice, bob, carol, alice.
	want := []string{"alice", "bob", "carol", "alice"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func TestScriptCompileError(t *testing.T) {
	_, err := script.New(&loadifyv1.ScriptBundle{MainJs: "function ("}, nil, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err == nil {
		t.Fatal("expected a compile error")
	}
}

// jsString quotes a Go string as a JS string literal.
func jsString(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		default:
			out = append(out, []byte(string(r))...)
		}
	}
	return string(append(out, '"'))
}
