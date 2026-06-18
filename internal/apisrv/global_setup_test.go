package apisrv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"google.golang.org/grpc"
)

// capturingCoord records the plan dispatched to workers so a test can assert
// what was actually sent.
type capturingCoord struct {
	fakeCoord
	lastPlan json.RawMessage
}

func (c *capturingCoord) StartRun(_ context.Context, req *loadifyv1.StartRunRequest, _ ...grpc.CallOption) (*loadifyv1.StartRunResponse, error) {
	c.lastPlan = req.PlanJson
	return &loadifyv1.StartRunResponse{RunId: "run-1", AssignedWorkers: 1}, nil
}

// TestLaunchRunFoldsGlobalSetupVars verifies that launching a scenario with a
// once_global setup step runs the setup once at launch and substitutes the
// extracted value into the dispatched plan, so every worker sees the resolved
// literal instead of the {{token}} template (no per-iteration login).
func TestLaunchRunFoldsGlobalSetupVars(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			_, _ = w.Write([]byte(`{"token":"SECRET"}`))
		case "/api/SECRET":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer target.Close()

	planJSON := fmt.Sprintf(`{"protocol":"scenario","scenario":{"mode":"sequence","steps":[
		{"method":"POST","url":"%s/login","scope":"once_global","extracts":[{"var":"token","path":"token"}]},
		{"method":"GET","url":"%s/api/{{token}}"}
	]}}`, target.URL, target.URL)

	meta := newFakeMeta()
	meta.testPlan = json.RawMessage(planJSON)
	coord := &capturingCoord{}
	srv := newTestServer(meta, coord)

	runID, runStatus, err := srv.launchRun(context.Background(), "test-1", 1, "", nil, "manual", "")
	if err != nil {
		t.Fatalf("launchRun: %v", err)
	}
	if runID == "" || runStatus != "running" {
		t.Fatalf("launchRun returned id=%q status=%q", runID, runStatus)
	}
	got := string(coord.lastPlan)
	if !strings.Contains(got, "/api/SECRET") {
		t.Errorf("dispatched plan did not resolve {{token}} to the setup value: %s", got)
	}
	if strings.Contains(got, "{{token}}") {
		t.Errorf("dispatched plan still contains the unresolved template: %s", got)
	}
}

// TestLaunchRunGlobalSetupFailureAborts verifies a failed setup step aborts the
// launch rather than dispatching a broken run.
func TestLaunchRunGlobalSetupFailureAborts(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	planJSON := fmt.Sprintf(`{"protocol":"scenario","scenario":{"mode":"sequence","steps":[
		{"method":"GET","url":"%s/login","scope":"once_global","extracts":[{"var":"token","path":"token"}]},
		{"method":"GET","url":"%s/api/{{token}}"}
	]}}`, target.URL, target.URL)

	meta := newFakeMeta()
	meta.testPlan = json.RawMessage(planJSON)
	coord := &capturingCoord{}
	srv := newTestServer(meta, coord)

	if _, _, err := srv.launchRun(context.Background(), "test-1", 1, "", nil, "manual", ""); err == nil {
		t.Fatal("expected launch to fail when global setup fails, got nil")
	}
	if coord.lastPlan != nil {
		t.Errorf("a run was dispatched despite setup failure: %s", coord.lastPlan)
	}
}
