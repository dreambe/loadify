package apisrv

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// debugBodyCap bounds how much of the response body a debug call returns.
const debugBodyCap = 64 << 10

type debugRequest struct {
	Method             string            `json:"method"`
	URL                string            `json:"url"`
	Headers            map[string]string `json:"headers,omitempty"`
	Body               string            `json:"body,omitempty"`
	TimeoutMs          int64             `json:"timeout_ms,omitempty"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify,omitempty"`
}

type debugResponse struct {
	Status        int               `json:"status"`
	StatusText    string            `json:"status_text"`
	LatencyMs     float64           `json:"latency_ms"`
	Headers       map[string]string `json:"headers"`
	Body          string            `json:"body"`
	BodyTruncated bool              `json:"body_truncated"`
	RecvBytes     int64             `json:"recv_bytes"`
	Error         string            `json:"error,omitempty"`
}

// handleDebugRequest fires the request being authored in the test builder once
// and returns the full response, so a test can be verified before it is saved
// and assertions can be written against a real payload.
func (s *Server) handleDebugRequest(w http.ResponseWriter, r *http.Request) {
	var req debugRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.URL == "" {
		writeErr(w, http.StatusBadRequest, "url is required")
		return
	}
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 15 * time.Second
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: req.InsecureSkipVerify}, //nolint:gosec // opt-in, mirrors the load driver
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()

	var body io.Reader
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(r.Context(), req.Method, req.URL, body)
	if err != nil {
		writeJSON(w, http.StatusOK, debugResponse{Error: err.Error()})
		return
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		writeJSON(w, http.StatusOK, debugResponse{
			LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
			Error:     err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	head := make([]byte, debugBodyCap)
	hn, _ := io.ReadFull(resp.Body, head)
	rest, _ := io.Copy(io.Discard, resp.Body)
	latency := float64(time.Since(start).Microseconds()) / 1000.0

	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	writeJSON(w, http.StatusOK, debugResponse{
		Status:        resp.StatusCode,
		StatusText:    http.StatusText(resp.StatusCode),
		LatencyMs:     latency,
		Headers:       headers,
		Body:          string(head[:hn]),
		BodyTruncated: rest > 0,
		RecvBytes:     int64(hn) + rest,
	})
}
