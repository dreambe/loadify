package apisrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// webhookTimeout bounds a single notification delivery.
const webhookTimeout = 10 * time.Second

// resolveWebhook picks the notification target for a run: the run creator's
// first configured webhook, falling back to the instance-wide env webhook.
func (s *Server) resolveWebhook(ctx context.Context, createdBy *string) string {
	if createdBy != nil && *createdBy != "" {
		if u, err := s.pg.GetUserByID(ctx, *createdBy); err == nil && len(u.WebhookURLs) > 0 {
			return u.WebhookURLs[0]
		}
	}
	return s.webhookURL
}

// notifyWebhook delivers a run-finished notification. Feishu/Lark webhooks get
// a nicely formatted interactive card; any other receiver gets a generic JSON
// event. Failures are logged, never fatal.
func (s *Server) notifyWebhook(runID, status string, payload map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), webhookTimeout)
	defer cancel()

	var createdBy *string
	name := runID
	if run, err := s.pg.GetRun(ctx, runID); err == nil {
		createdBy = run.CreatedBy
		if run.Name != "" {
			name = run.Name
		}
	}
	url := s.resolveWebhook(ctx, createdBy)
	if url == "" {
		return
	}

	var body []byte
	if isFeishu(url) {
		body, _ = json.Marshal(feishuCard(name, runID, status, payload, s.frontendURL))
	} else {
		body, _ = json.Marshal(map[string]any{
			"event":   "run.finished",
			"run_id":  runID,
			"name":    name,
			"status":  status,
			"details": payload,
			"ts":      time.Now().UTC().Format(time.RFC3339),
		})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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

func isFeishu(url string) bool {
	return strings.Contains(url, "open.feishu.cn") || strings.Contains(url, "open.larksuite.com") ||
		strings.Contains(url, "larksuite.com/open-apis/bot") || strings.Contains(url, "feishu.cn/open-apis/bot")
}

// feishuCard builds a Feishu/Lark interactive message card summarizing a run.
func feishuCard(name, runID, status string, payload map[string]any, frontendURL string) map[string]any {
	tmpl := "blue"
	emoji := "✅"
	switch status {
	case "failed":
		tmpl, emoji = "red", "❌"
	case "aborted":
		tmpl, emoji = "orange", "🛑"
	}

	var lines []string
	if total, ok := numField(payload, "total_requests"); ok {
		lines = append(lines, fmt.Sprintf("**总请求数 / Total:** %.0f", total))
	}
	if sm, ok := payload["summary"].(map[string]any); ok {
		if v, ok := numField(sm, "error_rate"); ok {
			lines = append(lines, fmt.Sprintf("**错误率 / Error rate:** %.2f%%", v*100))
		}
		if v, ok := numField(sm, "p95_ms"); ok {
			lines = append(lines, fmt.Sprintf("**p95:** %.1f ms", v))
		}
		if v, ok := numField(sm, "p99_ms"); ok {
			lines = append(lines, fmt.Sprintf("**p99:** %.1f ms", v))
		}
	}
	if reason, ok := payload["reason"].(string); ok && reason != "" {
		lines = append(lines, "**中止原因 / Reason:** "+reason)
	}
	if passed, ok := payload["passed"].(bool); ok {
		if passed {
			lines = append(lines, "**SLA:** ✅ 通过 PASSED")
		} else {
			lines = append(lines, "**SLA:** ❌ 未通过 FAILED")
		}
	}
	content := strings.Join(lines, "\n")
	if content == "" {
		content = "_(no metrics)_"
	}

	elements := []map[string]any{
		{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": content}},
	}
	if frontendURL != "" {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []map[string]any{{
				"tag":  "button",
				"text": map[string]any{"tag": "plain_text", "content": "查看详情 / Open run"},
				"type": "primary",
				"url":  strings.TrimRight(frontendURL, "/") + "/runs/" + runID,
			}},
		})
	}

	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"config": map[string]any{"wide_screen_mode": true},
			"header": map[string]any{
				"template": tmpl,
				"title":    map[string]any{"tag": "plain_text", "content": fmt.Sprintf("%s Loadify · %s (%s)", emoji, name, status)},
			},
			"elements": elements,
		},
	}
}

func numField(m map[string]any, k string) (float64, bool) {
	switch v := m[k].(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
