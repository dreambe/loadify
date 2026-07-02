package apisrv

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/script"
	"github.com/dreambe/loadify/internal/worker/protocols"
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
			// SSRF guard: this fetch runs on the apisrv host, so block never-legit
			// targets — loopback and link-local (incl. cloud metadata 169.254.169.254).
			// RFC1918 private ranges stay allowed: load-testing internal services is
			// the whole point. The check is on the *resolved* IP, defeating DNS rebinding.
			DialContext: (&net.Dialer{Timeout: timeout, Control: debugDialControl}).DialContext,
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

// debugMaxSteps bounds how many steps a scenario debug call will execute.
const debugMaxSteps = 20

type debugScenarioRequest struct {
	Steps []plan.ScenarioStep `json:"steps"`
}

type debugScenarioStep struct {
	Group     string  `json:"group"`
	Method    string  `json:"method"`
	URL       string  `json:"url"` // resolved (after {{var}} interpolation + query params)
	ReqBody   string  `json:"req_body,omitempty"`
	Status    int     `json:"status"`
	OK        bool    `json:"ok"`
	ErrorKind string  `json:"error_kind,omitempty"`
	LatencyMs float64 `json:"latency_ms"`
	Body      string  `json:"body"`
}

type debugScenarioResp struct {
	Steps []debugScenarioStep `json:"steps"`
	Error string              `json:"error,omitempty"`
}

// handleDebugScenario runs steps 1..N through the real scenario engine in order,
// performing {{var}} interpolation and extraction between steps, so a step that
// depends on an upstream value resolves correctly instead of firing the literal
// template (which otherwise looks like a spurious 404). It returns each step's
// resolved request and response. Single source of truth: it reuses the same
// goja harness as a live run, so debug behavior can't drift from production.
func (s *Server) handleDebugScenario(w http.ResponseWriter, r *http.Request) {
	var req debugScenarioRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.Steps) == 0 {
		writeErr(w, http.StatusBadRequest, "at least one step is required")
		return
	}
	if len(req.Steps) > debugMaxSteps {
		writeErr(w, http.StatusBadRequest, "too many steps to debug")
		return
	}
	// Force sequence mode and clear setup scopes: debug runs every posted step
	// once, in order, so the chain resolves and the user sees each step's
	// request — a setup step (once_per_vu/once_global) must not be skipped here.
	for i := range req.Steps {
		req.Steps[i].Scope = plan.ScopeEachIteration
	}
	sc := &plan.ScenarioConfig{Mode: "sequence", Steps: req.Steps}
	js, err := script.CompileScenario(sc)
	if err != nil {
		writeJSON(w, http.StatusOK, debugScenarioResp{Error: err.Error()})
		return
	}
	drv, err := script.New(&loadifyv1.ScriptBundle{MainJs: js}, &plan.Plan{}, loadifyv1.Protocol_PROTOCOL_UNSPECIFIED)
	if err != nil {
		writeJSON(w, http.StatusOK, debugScenarioResp{Error: err.Error()})
		return
	}
	md, ok := drv.(protocols.MultiDriver)
	if !ok {
		writeJSON(w, http.StatusOK, debugScenarioResp{Error: "scenario driver unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := drv.Prepare(ctx); err != nil {
		writeJSON(w, http.StatusOK, debugScenarioResp{Error: err.Error()})
		return
	}
	defer func() { _ = drv.Teardown(context.Background()) }()

	out := debugScenarioResp{Steps: make([]debugScenarioStep, 0, len(req.Steps))}
	for _, res := range md.ExecMulti(ctx, &protocols.VU{ID: 1}) {
		// Skip the synthetic transaction-total row a multi-step sequence emits.
		if strings.HasPrefix(res.Group, "txn:") {
			continue
		}
		body := res.RespBody
		if len(body) > debugBodyCap {
			body = body[:debugBodyCap]
		}
		out.Steps = append(out.Steps, debugScenarioStep{
			Group:     res.Group,
			Method:    res.Method,
			URL:       res.URL,
			ReqBody:   res.ReqBody,
			Status:    int(res.Status),
			OK:        res.OK,
			ErrorKind: res.ErrorKind,
			LatencyMs: float64(res.LatencyUs) / 1000.0,
			Body:      body,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// debugDialControl is the dial guard used by the debug fetch. It's a seam so
// tests (which can only bind loopback) can disable it; production keeps the
// SSRF block.
var debugDialControl = blockInternalDial

// blockInternalDial rejects connections to loopback and link-local addresses
// (including the cloud metadata endpoint 169.254.169.254) — SSRF targets that
// are never a legitimate load-test destination. It runs on the *resolved*
// address, so it also defeats DNS rebinding. RFC1918 private ranges are allowed
// on purpose: testing internal services is the point of the tool.
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("blocked internal address %s (loopback/link-local)", ip)
	}
	return nil
}
