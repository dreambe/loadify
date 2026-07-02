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
	// The interpolated plan (what actually ran) is passed in, but the snapshot is
	// served to any viewer and via public share links, so it must NOT embed the
	// substituted secret ("api.prod.com") — it keeps the {{base_url}} template.
	resolved := json.RawMessage(`{"protocol":"http","http":{"url":"https://api.prod.com/cart"}}`)
	snap := buildRunSnapshot(td, resolved, "", "prod", map[string]string{"base_url": "api.prod.com"})

	var m map[string]any
	if err := json.Unmarshal(snap, &m); err != nil {
		t.Fatalf("snapshot not valid json: %v", err)
	}

	// The snapshot keeps the template and must never leak the resolved value.
	planBytes, _ := json.Marshal(m["plan"])
	if strings.Contains(string(planBytes), "api.prod.com") {
		t.Errorf("snapshot plan leaked the resolved (secret) target: %s", planBytes)
	}
	if !strings.Contains(string(planBytes), "{{base_url}}") {
		t.Errorf("snapshot plan should keep the template: %s", planBytes)
	}

	// The environment name + keys are captured for reproducibility, but VALUES
	// are masked — the raw secret must not be persisted.
	env, ok := m["environment"].(map[string]any)
	if !ok {
		t.Fatal("snapshot missing environment block")
	}
	if env["name"] != "prod" {
		t.Errorf("environment name = %v, want prod", env["name"])
	}
	vars, ok := env["vars"].(map[string]any)
	if !ok {
		t.Fatalf("environment vars missing: %v", env["vars"])
	}
	if _, present := vars["base_url"]; !present {
		t.Errorf("env var key not preserved: %v", vars)
	}
	if v, _ := vars["base_url"].(string); strings.Contains(v, "api.prod.com") {
		t.Errorf("env var value leaked instead of being masked: %v", vars)
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
