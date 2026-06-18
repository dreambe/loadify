package apisrv

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsFeishu(t *testing.T) {
	if !isFeishu("https://open.feishu.cn/open-apis/bot/v2/hook/abc") {
		t.Error("feishu URL not detected")
	}
	if isFeishu("https://hooks.slack.com/services/x") {
		t.Error("slack URL wrongly detected as feishu")
	}
}

func TestFeishuCard(t *testing.T) {
	payload := map[string]any{
		"total_requests": float64(1200),
		"summary":        map[string]any{"error_rate": 0.05, "p95_ms": 180.0},
		"passed":         false,
		"reason":         "auto-stopped: error rate 80% > 50% over 10s",
	}
	b, _ := json.Marshal(feishuCard("checkout", "run-123", "aborted", payload, "https://loadify.example.com"))
	s := string(b)
	for _, want := range []string{"interactive", "checkout", "aborted", "auto-stopped", "5.00%", "/runs/run-123", "orange"} {
		if !strings.Contains(s, want) {
			t.Errorf("card missing %q\n%s", want, s)
		}
	}
}

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
