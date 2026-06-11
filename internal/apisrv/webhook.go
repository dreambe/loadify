package apisrv

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// webhookTimeout bounds a single notification delivery.
const webhookTimeout = 10 * time.Second

// notifyWebhook POSTs a run-finished event to the configured webhook URL
// (Slack-compatible services, CI systems, or any HTTP receiver). Failures are
// logged, never fatal — notifications must not affect run finalization.
func (s *Server) notifyWebhook(runID, status string, payload map[string]any) {
	if s.webhookURL == "" {
		return
	}
	body, err := json.Marshal(map[string]any{
		"event":   "run.finished",
		"run_id":  runID,
		"status":  status,
		"details": payload,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), webhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		s.log.Warn("webhook: build request failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Warn("webhook: delivery failed", "run", runID, "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		s.log.Warn("webhook: non-2xx response", "run", runID, "status", resp.StatusCode)
	}
}
