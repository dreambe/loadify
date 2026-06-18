package apisrv

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dreambe/loadify/internal/store/postgres"
)

func TestBuildRunSnapshot(t *testing.T) {
	td := &postgres.TestDefinition{
		ID:       "t1",
		Name:     "checkout",
		Protocol: "http",
		PlanJSON: json.RawMessage(`{"protocol":"http","http":{"url":"https://{{base_url}}/cart"}}`),
		RampJSON: json.RawMessage(`[{"duration_ms":30000,"target_vus":50}]`),
	}
	// Resolved plan (what actually ran) + the environment used.
	resolved := json.RawMessage(`{"protocol":"http","http":{"url":"https://api.prod.com/cart"}}`)
	snap := buildRunSnapshot(td, resolved, "", "prod", map[string]string{"base_url": "api.prod.com"})

	var m map[string]any
	if err := json.Unmarshal(snap, &m); err != nil {
		t.Fatalf("snapshot not valid json: %v", err)
	}

	// The snapshot must carry the resolved target, not the {{base_url}} template.
	planBytes, _ := json.Marshal(m["plan"])
	if !strings.Contains(string(planBytes), "api.prod.com") {
		t.Errorf("snapshot plan missing resolved target: %s", planBytes)
	}
	if strings.Contains(string(planBytes), "{{base_url}}") {
		t.Errorf("snapshot plan still templated: %s", planBytes)
	}

	// The environment used is captured so a later edit can't rewrite history.
	env, ok := m["environment"].(map[string]any)
	if !ok {
		t.Fatal("snapshot missing environment block")
	}
	if env["name"] != "prod" {
		t.Errorf("environment name = %v, want prod", env["name"])
	}
	if vars, ok := env["vars"].(map[string]any); !ok || vars["base_url"] != "api.prod.com" {
		t.Errorf("environment vars not snapshotted: %v", env["vars"])
	}

	// Identity fields are preserved; ramp survives for the load-model detector.
	if m["name"] != "checkout" || m["protocol"] != "http" {
		t.Errorf("identity fields lost: %v / %v", m["name"], m["protocol"])
	}
	if _, ok := m["ramp"]; !ok {
		t.Error("ramp missing from snapshot")
	}
}

func TestBuildRunSnapshotNoEnv(t *testing.T) {
	td := &postgres.TestDefinition{
		ID: "t1", Name: "n", Protocol: "http",
		PlanJSON: json.RawMessage(`{"protocol":"http","http":{"url":"http://x"}}`),
		RampJSON: json.RawMessage(`[]`),
	}
	snap := buildRunSnapshot(td, td.PlanJSON, "", "", nil)
	var m map[string]any
	if err := json.Unmarshal(snap, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["environment"]; ok {
		t.Error("no-env run should not carry an environment block")
	}
}
