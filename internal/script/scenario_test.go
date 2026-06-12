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
// iteration against a real test server, returning the Result.
func runScenario(t *testing.T, sc *plan.ScenarioConfig) protocols.Result {
	t.Helper()
	js, err := CompileScenario(sc)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	d, err := New(&loadifyv1.ScriptBundle{MainJs: js}, &plan.Plan{}, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	if err := d.Prepare(t.Context()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer d.Teardown(t.Context())
	return d.Exec(t.Context(), &protocols.VU{ID: 1})
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
	res := runScenario(t, sc)
	if !res.OK {
		t.Fatalf("scenario failed: %+v", res)
	}
	if got := sawToken.Load(); got != "Bearer abc123" {
		t.Errorf("chained header = %v, want Bearer abc123", got)
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
