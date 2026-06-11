package apisrv

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFinalizeRunFiresWebhook(t *testing.T) {
	got := make(chan map[string]any, 1)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		got <- m
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	meta := newFakeMeta()
	srv := New(Config{
		Postgres:    meta,
		ClickHouse:  fakeMetrics{},
		Coordinator: &fakeCoord{},
		JWTSecret:   "s",
		WebhookURL:  hook.URL,
	})

	srv.finalizeRun("run-9", "completed")

	select {
	case m := <-got:
		if m["event"] != "run.finished" || m["run_id"] != "run-9" || m["status"] != "completed" {
			t.Errorf("unexpected webhook payload: %v", m)
		}
	default:
		t.Fatal("webhook was not delivered")
	}

	// Finalizing again is a no-op (idempotent), so no second delivery.
	srv.finalizeRun("run-9", "completed")
	select {
	case m := <-got:
		t.Errorf("unexpected second delivery: %v", m)
	default:
	}
}
