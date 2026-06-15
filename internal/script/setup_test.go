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

// TestScenarioOncePerVUSetup verifies a once_per_vu setup step runs a single
// time for a VU (not every iteration) and that its extracted variable is
// available to the workload steps on every iteration.
func TestScenarioOncePerVUSetup(t *testing.T) {
	var logins int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			atomic.AddInt64(&logins, 1)
			_, _ = w.Write([]byte(`{"token":"T"}`))
		case "/api/T":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	sc := &plan.ScenarioConfig{Mode: "sequence", Steps: []plan.ScenarioStep{
		{Method: "POST", URL: srv.URL + "/login", Scope: plan.ScopeOncePerVU,
			Extracts: []plan.ScenarioExtract{{Var: "token", Path: "token"}}},
		{Method: "GET", URL: srv.URL + "/api/{{token}}"},
	}}
	js, err := CompileScenario(sc)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	d, err := New(&loadifyv1.ScriptBundle{MainJs: js}, &plan.Plan{}, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	md := d.(protocols.MultiDriver)
	if err := d.Prepare(t.Context()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer d.Teardown(t.Context())

	vu := &protocols.VU{ID: 1}
	// First iteration: setup login + workload step.
	first := md.ExecMulti(t.Context(), vu)
	// Second iteration: workload only, setup must NOT re-run.
	second := md.ExecMulti(t.Context(), vu)

	if got := atomic.LoadInt64(&logins); got != 1 {
		t.Fatalf("login ran %d times, want exactly 1 (once per VU)", got)
	}
	// The workload step must resolve {{token}} on every iteration (200, not 404).
	for label, res := range map[string][]protocols.Result{"first": first, "second": second} {
		var saw bool
		for _, r := range res {
			if strings.HasSuffix(r.URL, "/api/T") {
				saw = true
				if r.Status != 200 || !r.OK {
					t.Errorf("%s: workload step = %d ok=%v, want 200 ok", label, r.Status, r.OK)
				}
			}
		}
		if !saw {
			t.Errorf("%s iteration did not run the workload step: %+v", label, res)
		}
	}
}

// TestRunGlobalSetup verifies global setup runs the once_global steps and
// returns the variables they extract, so a launcher can fold them into env.
func TestRunGlobalSetup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			_, _ = w.Write([]byte(`{"data":{"token":"GLOBAL-TOKEN"},"uid":42}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	steps := []plan.ScenarioStep{
		{Method: "POST", URL: srv.URL + "/login", Scope: plan.ScopeOnceGlobal, Extracts: []plan.ScenarioExtract{
			{Var: "token", Path: "data.token"},
			{Var: "uid", Path: "uid"},
		}},
	}
	vars, err := RunGlobalSetup(t.Context(), steps)
	if err != nil {
		t.Fatalf("RunGlobalSetup: %v", err)
	}
	if vars["token"] != "GLOBAL-TOKEN" {
		t.Errorf("token = %q, want GLOBAL-TOKEN", vars["token"])
	}
	if vars["uid"] != "42" {
		t.Errorf("uid = %q, want 42", vars["uid"])
	}
}

// TestRunGlobalSetupFailureAborts verifies a failed setup step surfaces as an
// error so the launcher can abort instead of starting a broken run.
func TestRunGlobalSetupFailureAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := RunGlobalSetup(t.Context(), []plan.ScenarioStep{
		{Method: "GET", URL: srv.URL + "/login", Scope: plan.ScopeOnceGlobal},
	})
	if err == nil {
		t.Fatal("expected error from failing setup step, got nil")
	}
}

// TestGlobalSetupStepsFilters checks only once_global steps are selected, in order.
func TestGlobalSetupStepsFilters(t *testing.T) {
	sc := &plan.ScenarioConfig{Steps: []plan.ScenarioStep{
		{Name: "a", Scope: plan.ScopeOnceGlobal},
		{Name: "b"},
		{Name: "c", Scope: plan.ScopeOncePerVU},
		{Name: "d", Scope: plan.ScopeOnceGlobal},
	}}
	got := GlobalSetupSteps(sc)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "d" {
		t.Fatalf("GlobalSetupSteps = %+v, want [a d]", got)
	}
}
