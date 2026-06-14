package script

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

// runScenario compiles a scenario, builds the script driver and runs one
// iteration against a real test server, returning every emitted result.
func runScenario(t *testing.T, sc *plan.ScenarioConfig) []protocols.Result {
	t.Helper()
	js, err := CompileScenario(sc)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	d, err := New(&loadifyv1.ScriptBundle{MainJs: js}, &plan.Plan{}, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	md := d.(protocols.MultiDriver)
	if err := d.Prepare(t.Context()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer d.Teardown(t.Context())
	return md.ExecMulti(t.Context(), &protocols.VU{ID: 1})
}

func TestScenarioSequenceChaining(t *testing.T) {
	var sawToken atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"data":{"token":"abc123"}}`))
		case "/me":
			sawToken.Store(r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	sc := &plan.ScenarioConfig{
		Mode: "sequence",
		Steps: []plan.ScenarioStep{
			{
				Name: "login", Method: "POST", URL: srv.URL + "/login",
				Extracts: []plan.ScenarioExtract{{Var: "token", Path: "data.token"}},
				Asserts:  []plan.HTTPAssert{{Source: "status", Op: "eq", Value: "200"}},
			},
			{
				Name: "me", Method: "GET", URL: srv.URL + "/me",
				Headers: map[string]string{"Authorization": "Bearer {{token}}"},
				Asserts: []plan.HTTPAssert{{Source: "json", Path: "ok", Op: "eq", Value: "true"}},
			},
		},
	}
	results := runScenario(t, sc)
	// Two steps + one transaction total.
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 (2 steps + txn): %+v", len(results), results)
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("result %s not ok: %+v", r.Group, r)
		}
	}
	if results[0].Group != "login" || results[1].Group != "me" {
		t.Errorf("step groups = %q,%q want login,me", results[0].Group, results[1].Group)
	}
	if results[2].Group != "txn:scenario" {
		t.Errorf("txn group = %q, want txn:scenario", results[2].Group)
	}
	// Transaction latency is the sum of the steps (end-to-end).
	if results[2].LatencyUs < results[0].LatencyUs {
		t.Errorf("txn latency %d should be >= first step %d", results[2].LatencyUs, results[0].LatencyUs)
	}
	if got := sawToken.Load(); got != "Bearer abc123" {
		t.Errorf("chained header = %v, want Bearer abc123", got)
	}
}

func TestScenarioExtractFailureSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			_, _ = w.Write([]byte(`{"data":{"id":1}}`))
		default: // non-JSON body
			_, _ = w.Write([]byte(`not json at all`))
		}
	}))
	defer srv.Close()

	// A path that doesn't exist in valid JSON → extract_missing, step fails.
	missing := runScenario(t, &plan.ScenarioConfig{
		Mode: "sequence",
		Steps: []plan.ScenarioStep{{
			Name: "s", Method: "GET", URL: srv.URL + "/json",
			Extracts: []plan.ScenarioExtract{{Var: "tok", Path: "data.token"}},
		}},
	})
	if missing[0].OK || missing[0].ErrorKind != "extract_missing" {
		t.Errorf("missing path: got ok=%v kind=%q, want ok=false kind=extract_missing", missing[0].OK, missing[0].ErrorKind)
	}

	// A present path still succeeds.
	present := runScenario(t, &plan.ScenarioConfig{
		Mode: "sequence",
		Steps: []plan.ScenarioStep{{
			Name: "s", Method: "GET", URL: srv.URL + "/json",
			Extracts: []plan.ScenarioExtract{{Var: "id", Path: "data.id"}},
		}},
	})
	if !present[0].OK {
		t.Errorf("present path: got ok=false kind=%q, want ok=true", present[0].ErrorKind)
	}

	// Unparsable body with an extract configured → extract_failed.
	unparsable := runScenario(t, &plan.ScenarioConfig{
		Mode: "sequence",
		Steps: []plan.ScenarioStep{{
			Name: "s", Method: "GET", URL: srv.URL + "/text",
			Extracts: []plan.ScenarioExtract{{Var: "x", Path: "a"}},
		}},
	})
	if unparsable[0].OK || unparsable[0].ErrorKind != "extract_failed" {
		t.Errorf("unparsable body: got ok=%v kind=%q, want ok=false kind=extract_failed", unparsable[0].OK, unparsable[0].ErrorKind)
	}
}

func TestScenarioTemplateFunctions(t *testing.T) {
	var gotPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	sc := &plan.ScenarioConfig{
		Mode:  "sequence",
		Steps: []plan.ScenarioStep{{Method: "GET", URL: srv.URL + "/u/{{uuid}}"}},
	}
	runScenario(t, sc)
	p, _ := gotPath.Load().(string)
	// {{uuid}} should have been replaced by a 36-char UUID, not left literal.
	if strings.Contains(p, "{{") || len(p) < len("/u/")+30 {
		t.Errorf("template function not interpolated: %q", p)
	}
}

func TestScenarioWeightedSelectsOneStep(t *testing.T) {
	var a, b atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			a.Add(1)
		} else {
			b.Add(1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	sc := &plan.ScenarioConfig{
		Mode: "weighted",
		Steps: []plan.ScenarioStep{
			{Name: "a", Method: "GET", URL: srv.URL + "/a", Weight: 1},
			{Name: "b", Method: "GET", URL: srv.URL + "/b", Weight: 1},
		},
	}
	js, err := CompileScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := New(&loadifyv1.ScriptBundle{MainJs: js}, &plan.Plan{}, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	_ = d.Prepare(t.Context())
	defer d.Teardown(t.Context())
	for i := 0; i < 50; i++ {
		d.Exec(t.Context(), &protocols.VU{ID: 1})
	}
	// Exactly one request fires per iteration → totals sum to 50.
	if a.Load()+b.Load() != 50 {
		t.Errorf("weighted fired %d+%d, want 50 total (one per iteration)", a.Load(), b.Load())
	}
	if a.Load() == 0 || b.Load() == 0 {
		t.Errorf("weighted never picked one branch: a=%d b=%d", a.Load(), b.Load())
	}
}

func TestCompileScenarioValidJS(t *testing.T) {
	js, err := CompileScenario(&plan.ScenarioConfig{
		Mode:  "sequence",
		Steps: []plan.ScenarioStep{{Method: "GET", URL: "http://x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, "function iteration()") {
		t.Error("compiled script missing iteration()")
	}
}
