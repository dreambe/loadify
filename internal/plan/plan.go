// Package plan defines the declarative test-plan model that is authored in the
// UI/JSON, validated, and compiled into an executable run for the workers.
package plan

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Protocol mirrors loadify.v1.Protocol as a JSON-friendly string.
type Protocol string

const (
	HTTP      Protocol = "http"
	HTTPS     Protocol = "https"
	GRPC      Protocol = "grpc"
	WebSocket Protocol = "websocket"
	SSE       Protocol = "sse"
	// Script marks a plan whose traffic is generated entirely by a goja script
	// (see ScriptBundle); the script issues its own requests, so the plan needs
	// no protocol-specific target config.
	Script Protocol = "script"
	// Scenario is a no-code multi-step HTTP plan: a list of steps run either in
	// sequence (with variable extraction/chaining) or chosen by weight (traffic
	// mix). It is compiled to a script bundle at launch and runs on the script
	// driver, so the worker needs no separate scenario driver.
	Scenario Protocol = "scenario"
)

// Plan is the top-level test definition.
type Plan struct {
	Protocol Protocol    `json:"protocol"`
	Name     string      `json:"name,omitempty"`
	HTTP     *HTTPConfig `json:"http,omitempty"`
	GRPC     *GRPCConfig `json:"grpc,omitempty"`
	WS       *WSConfig   `json:"websocket,omitempty"`
	SSE      *SSEConfig  `json:"sse,omitempty"`
	Scenario *ScenarioConfig `json:"scenario,omitempty"`
	// ThinkTimeMs is the per-iteration pause applied after each request (fixed).
	ThinkTimeMs int64 `json:"think_time_ms,omitempty"`
	// ThinkTimeCfg, when set, overrides ThinkTimeMs with a randomized distribution.
	ThinkTimeCfg *ThinkTimeConfig `json:"think_time,omitempty"`
	// Rendezvous, when set, holds VUs at a barrier until N are ready, then
	// releases them together (a sync point / 集合点) to model burst concurrency.
	Rendezvous *RendezvousConfig `json:"rendezvous,omitempty"`
	// AutoStop is the safety circuit breaker; nil means "enabled with defaults".
	AutoStop *AutoStopConfig `json:"auto_stop,omitempty"`
	// Alert fires a one-shot mid-run notification when the error rate spikes —
	// an early warning before the auto-stop breaker trips. nil = enabled default.
	Alert *AlertConfig `json:"alert,omitempty"`
	// MaxVUs caps the worker pool for the open (arrival-rate) model. 0 lets the
	// worker derive a safe bound from the peak target rate.
	MaxVUs int `json:"max_vus,omitempty"`
	// ScriptTimeoutMs bounds a single goja iteration; an iteration exceeding it
	// is interrupted and counted as a failure rather than hanging the VU. 0 uses
	// the default (DefaultScriptTimeout).
	ScriptTimeoutMs int64 `json:"script_timeout_ms,omitempty"`
	// MaxRequestBodyBytes caps the size of any request body template. 0 uses the
	// default (DefaultMaxRequestBody).
	MaxRequestBodyBytes int `json:"max_request_body_bytes,omitempty"`
}

// DefaultScriptTimeout bounds a single script iteration so an infinite loop in
// user JS interrupts instead of pinning a worker core forever.
const DefaultScriptTimeout = 30 * time.Second

// DefaultMaxRequestBody caps a request body template (1 MiB) so a pathological
// plan can't balloon worker memory. Raise per-plan via MaxRequestBodyBytes.
const DefaultMaxRequestBody = 1 << 20

// ScriptTimeout returns the effective per-iteration script timeout.
func (p *Plan) ScriptTimeout() time.Duration {
	if p.ScriptTimeoutMs > 0 {
		return time.Duration(p.ScriptTimeoutMs) * time.Millisecond
	}
	return DefaultScriptTimeout
}

// maxBodyBytes returns the effective request-body size cap.
func (p *Plan) maxBodyBytes() int {
	if p.MaxRequestBodyBytes > 0 {
		return p.MaxRequestBodyBytes
	}
	return DefaultMaxRequestBody
}

// ThinkTimeConfig describes a randomized per-iteration pause.
//
//	distribution: fixed | uniform | gaussian | poisson
//	fixed     → MinMs
//	uniform   → [MinMs, MaxMs]
//	gaussian  → mean MeanMs, std-dev StddevMs (clamped ≥ 0)
//	poisson   → mean MeanMs (exponential inter-arrival)
type ThinkTimeConfig struct {
	Distribution string `json:"distribution"`
	MinMs        int64  `json:"min_ms,omitempty"`
	MaxMs        int64  `json:"max_ms,omitempty"`
	MeanMs       int64  `json:"mean_ms,omitempty"`
	StddevMs     int64  `json:"stddev_ms,omitempty"`
}

// RendezvousConfig is a per-worker sync point: each iteration waits until VUs
// VUs are gathered (or TimeoutMs elapses) before firing, modeling bursts.
type RendezvousConfig struct {
	VUs       int   `json:"vus"`
	TimeoutMs int64 `json:"timeout_ms,omitempty"`
}

// AutoStopConfig is the safety circuit breaker. It aborts a run when, over a
// trailing window, the error rate exceeds a threshold — preventing a runaway
// test from hammering an already-failing target. Enabled by default.
type AutoStopConfig struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	ErrorRatePct float64 `json:"error_rate_pct,omitempty"`
	WindowSec    int     `json:"window_sec,omitempty"`
	MinRequests  int     `json:"min_requests,omitempty"`
}

// AutoStopOrDefault returns the effective auto-stop config: a nil plan field
// means enabled with safe defaults (abort at >50% errors over 10s once at
// least 20 requests have been seen).
func (p *Plan) AutoStopOrDefault() AutoStopConfig {
	c := AutoStopConfig{ErrorRatePct: 50, WindowSec: 10, MinRequests: 20}
	if p.AutoStop != nil {
		if p.AutoStop.Enabled != nil && !*p.AutoStop.Enabled {
			return AutoStopConfig{Enabled: p.AutoStop.Enabled}
		}
		if p.AutoStop.ErrorRatePct > 0 {
			c.ErrorRatePct = p.AutoStop.ErrorRatePct
		}
		if p.AutoStop.WindowSec > 0 {
			c.WindowSec = p.AutoStop.WindowSec
		}
		if p.AutoStop.MinRequests > 0 {
			c.MinRequests = p.AutoStop.MinRequests
		}
	}
	on := true
	c.Enabled = &on
	return c
}

// AutoStopEnabled reports whether the breaker is on (default true).
func (c AutoStopConfig) AutoStopEnabled() bool { return c.Enabled == nil || *c.Enabled }

// AlertConfig fires a one-shot notification mid-run when the trailing-window
// error rate crosses a threshold — an early warning before auto-stop aborts.
// Enabled by default at a threshold below the auto-stop default.
type AlertConfig struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	ErrorRatePct float64 `json:"error_rate_pct,omitempty"`
	WindowSec    int     `json:"window_sec,omitempty"`
	MinRequests  int     `json:"min_requests,omitempty"`
}

// AlertOrDefault returns the effective alert config: a nil field means enabled
// with safe defaults (notify at >30% errors over 10s once 20 requests are seen
// — below the 50% auto-stop default, so it warns before the breaker trips).
func (p *Plan) AlertOrDefault() AlertConfig {
	c := AlertConfig{ErrorRatePct: 30, WindowSec: 10, MinRequests: 20}
	if p.Alert != nil {
		if p.Alert.Enabled != nil && !*p.Alert.Enabled {
			return AlertConfig{Enabled: p.Alert.Enabled}
		}
		if p.Alert.ErrorRatePct > 0 {
			c.ErrorRatePct = p.Alert.ErrorRatePct
		}
		if p.Alert.WindowSec > 0 {
			c.WindowSec = p.Alert.WindowSec
		}
		if p.Alert.MinRequests > 0 {
			c.MinRequests = p.Alert.MinRequests
		}
	}
	on := true
	c.Enabled = &on
	return c
}

// AlertEnabled reports whether mid-run alerting is on (default true).
func (c AlertConfig) AlertEnabled() bool { return c.Enabled == nil || *c.Enabled }

// HTTPConfig describes a single HTTP/HTTPS request template.
type HTTPConfig struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	TimeoutMs int64             `json:"timeout_ms,omitempty"`
	// DisableKeepAlive forces a fresh connection per request (cold-connection test).
	DisableKeepAlive bool `json:"disable_keepalive,omitempty"`
	// InsecureSkipVerify disables TLS certificate verification (default: verify).
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
	// ExpectStatus, when set, marks any other status as a failure.
	ExpectStatus int `json:"expect_status,omitempty"`
	// BodyContains, when set, fails the iteration unless the response body
	// contains this substring (checked against the first 256 KiB).
	BodyContains string `json:"body_contains,omitempty"`
	// Asserts are structured per-request checks (status / body / JSON field).
	Asserts []HTTPAssert `json:"asserts,omitempty"`
	Group   string       `json:"group,omitempty"`
}

// HTTPAssert is one per-request check evaluated against the response.
//
//   - source "status":  compares the HTTP status code
//   - source "body":    compares the raw body text (first 256 KiB)
//   - source "json":    extracts Path (dot notation, e.g. "data.items.0.id")
//     from the JSON body and compares the extracted value
//
// Ops: eq, ne, gt, lt, gte, lte, contains, exists. A missing JSON field or an
// unparsable body fails the assertion (with a descriptive reason) — it never
// aborts the run.
type HTTPAssert struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Op     string `json:"op"`
	Value  string `json:"value,omitempty"`
}

var validAssertOps = map[string]bool{
	"eq": true, "ne": true, "gt": true, "lt": true,
	"gte": true, "lte": true, "contains": true, "exists": true,
}

// Validate checks a single assertion definition.
func (a *HTTPAssert) Validate() error {
	switch a.Source {
	case "status", "body", "json":
	default:
		return fmt.Errorf("plan: assert source must be status/body/json, got %q", a.Source)
	}
	if !validAssertOps[a.Op] {
		return fmt.Errorf("plan: assert op %q not one of eq/ne/gt/lt/gte/lte/contains/exists", a.Op)
	}
	if a.Source == "status" {
		switch a.Op {
		case "eq", "ne", "gt", "lt", "gte", "lte":
		default:
			return fmt.Errorf("plan: status assert op must be numeric/equality (eq/ne/gt/lt/gte/lte), got %q", a.Op)
		}
	}
	if a.Source == "json" && a.Path == "" {
		return fmt.Errorf("plan: json assert requires a path")
	}
	if a.Op != "exists" && a.Value == "" && a.Source != "body" {
		return fmt.Errorf("plan: assert %s/%s requires a value", a.Source, a.Op)
	}
	return nil
}

// GRPCConfig describes a gRPC call (dynamic invocation by descriptor).
type GRPCConfig struct {
	Target             string            `json:"target"`
	FullMethod         string            `json:"full_method"` // /pkg.Svc/Method
	RequestJSON        string            `json:"request_json,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	DescriptorSet      []byte            `json:"descriptor_set,omitempty"`
	TimeoutMs          int64             `json:"timeout_ms,omitempty"`
	PlaintextProbe     bool              `json:"plaintext,omitempty"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify,omitempty"`
	// MaxMessages bounds how many responses a server-streaming call reads per
	// iteration (0 = until the server closes the stream or the timeout fires).
	MaxMessages int    `json:"max_messages,omitempty"`
	Group       string `json:"group,omitempty"`
}

// WSConfig describes a WebSocket session.
type WSConfig struct {
	URL                string            `json:"url"`
	Headers            map[string]string `json:"headers,omitempty"`
	SendMessages       []string          `json:"send_messages,omitempty"`
	ExpectEcho         bool              `json:"expect_echo,omitempty"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify,omitempty"`
	Group              string            `json:"group,omitempty"`
}

// SSEConfig describes a Server-Sent-Events stream.
type SSEConfig struct {
	URL                string            `json:"url"`
	Headers            map[string]string `json:"headers,omitempty"`
	MaxEvents          int               `json:"max_events,omitempty"`
	TimeoutMs          int64             `json:"timeout_ms,omitempty"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify,omitempty"`
	Group              string            `json:"group,omitempty"`
}

// ScenarioConfig is a multi-step HTTP plan.
//
//   - mode "sequence": every step runs in order; later steps can reference
//     variables extracted from earlier responses via {{var}} interpolation.
//   - mode "weighted": one step is chosen per iteration with probability
//     proportional to its weight, modeling a realistic traffic mix.
type ScenarioConfig struct {
	Mode  string         `json:"mode"`
	Steps []ScenarioStep `json:"steps"`
}

// ScenarioStep is one HTTP call in a scenario.
type ScenarioStep struct {
	Name     string            `json:"name,omitempty"`
	Weight   int               `json:"weight,omitempty"`
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Extracts []ScenarioExtract `json:"extracts,omitempty"`
	Asserts  []HTTPAssert      `json:"asserts,omitempty"`
}

// ScenarioExtract saves a JSON field from a step's response into a variable
// usable as {{Var}} in later steps (sequence mode).
type ScenarioExtract struct {
	Var  string `json:"var"`
	Path string `json:"path"`
}

func (c *ScenarioConfig) validate() error {
	if c == nil || len(c.Steps) == 0 {
		return fmt.Errorf("plan: scenario requires at least one step")
	}
	if c.Mode != "sequence" && c.Mode != "weighted" {
		return fmt.Errorf("plan: scenario mode must be sequence or weighted, got %q", c.Mode)
	}
	for i := range c.Steps {
		st := &c.Steps[i]
		if st.URL == "" {
			return fmt.Errorf("plan: scenario step %d requires a url", i+1)
		}
		if err := validateURL(st.URL, "http", "https"); err != nil {
			return fmt.Errorf("plan: scenario step %d: %w", i+1, err)
		}
		if st.Method == "" {
			st.Method = "GET"
		}
		if c.Mode == "weighted" && st.Weight <= 0 {
			st.Weight = 1
		}
		for j := range st.Asserts {
			if err := st.Asserts[j].Validate(); err != nil {
				return err
			}
		}
		for j := range st.Extracts {
			if st.Extracts[j].Var == "" || st.Extracts[j].Path == "" {
				return fmt.Errorf("plan: scenario step %d extract needs var and path", i+1)
			}
		}
	}
	return nil
}

// Parse decodes and validates a plan from JSON.
func Parse(data []byte) (*Plan, error) {
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("plan: invalid json: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks that the plan is internally consistent.
func (p *Plan) Validate() error {
	limit := p.maxBodyBytes()
	if err := p.validateNumerics(); err != nil {
		return err
	}
	switch p.Protocol {
	case HTTP, HTTPS:
		if p.HTTP == nil {
			return fmt.Errorf("plan: http config required for protocol %q", p.Protocol)
		}
		if p.HTTP.URL == "" {
			return fmt.Errorf("plan: http.url is required")
		}
		if err := validateURL(p.HTTP.URL, "http", "https"); err != nil {
			return err
		}
		if p.HTTP.Method == "" {
			p.HTTP.Method = "GET"
		}
		if p.HTTP.ExpectStatus != 0 && (p.HTTP.ExpectStatus < 100 || p.HTTP.ExpectStatus > 599) {
			return fmt.Errorf("plan: http.expect_status %d must be within [100,599]", p.HTTP.ExpectStatus)
		}
		if len(p.HTTP.Body) > limit {
			return fmt.Errorf("plan: http.body %d bytes exceeds limit %d", len(p.HTTP.Body), limit)
		}
		for i := range p.HTTP.Asserts {
			if err := p.HTTP.Asserts[i].Validate(); err != nil {
				return err
			}
		}
	case GRPC:
		if p.GRPC == nil || p.GRPC.Target == "" || p.GRPC.FullMethod == "" {
			return fmt.Errorf("plan: grpc target and full_method are required")
		}
		if err := validateHostPort(p.GRPC.Target); err != nil {
			return err
		}
		if p.GRPC.MaxMessages < 0 {
			return fmt.Errorf("plan: grpc.max_messages must be >= 0, got %d", p.GRPC.MaxMessages)
		}
		if len(p.GRPC.RequestJSON) > limit {
			return fmt.Errorf("plan: grpc.request_json %d bytes exceeds limit %d", len(p.GRPC.RequestJSON), limit)
		}
	case WebSocket:
		if p.WS == nil || p.WS.URL == "" {
			return fmt.Errorf("plan: websocket.url is required")
		}
		if err := validateURL(p.WS.URL, "ws", "wss", "http", "https"); err != nil {
			return err
		}
	case SSE:
		if p.SSE == nil || p.SSE.URL == "" {
			return fmt.Errorf("plan: sse.url is required")
		}
		if err := validateURL(p.SSE.URL, "http", "https"); err != nil {
			return err
		}
	case Script:
		// A script plan carries no protocol target; the script generates traffic.
	case Scenario:
		if err := p.Scenario.validate(); err != nil {
			return err
		}
		for i := range p.Scenario.Steps {
			if len(p.Scenario.Steps[i].Body) > limit {
				return fmt.Errorf("plan: scenario step %d body %d bytes exceeds limit %d", i+1, len(p.Scenario.Steps[i].Body), limit)
			}
		}
	default:
		return fmt.Errorf("plan: unknown protocol %q", p.Protocol)
	}
	return nil
}

// validateNumerics rejects out-of-range tunables at parse time instead of
// silently clamping or ignoring them at run time. Optional sub-configs are
// only checked when explicitly set (a nil field means "use defaults").
func (p *Plan) validateNumerics() error {
	if p.ThinkTimeMs < 0 {
		return fmt.Errorf("plan: think_time_ms must be >= 0, got %d", p.ThinkTimeMs)
	}
	if p.MaxVUs < 0 {
		return fmt.Errorf("plan: max_vus must be >= 0, got %d", p.MaxVUs)
	}
	if p.ScriptTimeoutMs < 0 {
		return fmt.Errorf("plan: script_timeout_ms must be >= 0, got %d", p.ScriptTimeoutMs)
	}
	if p.MaxRequestBodyBytes < 0 {
		return fmt.Errorf("plan: max_request_body_bytes must be >= 0, got %d", p.MaxRequestBodyBytes)
	}
	if t := p.ThinkTimeCfg; t != nil {
		if t.MinMs < 0 || t.MaxMs < 0 || t.MeanMs < 0 || t.StddevMs < 0 {
			return fmt.Errorf("plan: think_time values must be >= 0")
		}
		if t.Distribution == "uniform" && t.MaxMs < t.MinMs {
			return fmt.Errorf("plan: think_time uniform max_ms (%d) must be >= min_ms (%d)", t.MaxMs, t.MinMs)
		}
	}
	if r := p.Rendezvous; r != nil {
		if r.VUs < 0 {
			return fmt.Errorf("plan: rendezvous.vus must be >= 0, got %d", r.VUs)
		}
		if r.TimeoutMs < 0 {
			return fmt.Errorf("plan: rendezvous.timeout_ms must be >= 0, got %d", r.TimeoutMs)
		}
	}
	if c := p.AutoStop; c != nil {
		if err := validateBreakerFields("auto_stop", c.ErrorRatePct, c.WindowSec, c.MinRequests); err != nil {
			return err
		}
	}
	if c := p.Alert; c != nil {
		if err := validateBreakerFields("alert", c.ErrorRatePct, c.WindowSec, c.MinRequests); err != nil {
			return err
		}
	}
	return nil
}

// validateBreakerFields checks the shared knobs of the auto-stop and alert
// circuit breakers.
func validateBreakerFields(name string, errPct float64, windowSec, minReq int) error {
	if errPct < 0 || errPct > 100 {
		return fmt.Errorf("plan: %s.error_rate_pct must be within [0,100], got %g", name, errPct)
	}
	if windowSec < 0 {
		return fmt.Errorf("plan: %s.window_sec must be >= 0, got %d", name, windowSec)
	}
	if minReq < 0 {
		return fmt.Errorf("plan: %s.min_requests must be >= 0, got %d", name, minReq)
	}
	return nil
}

// validateURL checks that raw is a well-formed URL carrying a host and an
// allowed scheme, turning a run-time transport failure into a clear plan error.
// URLs carrying a {{template}} placeholder are accepted as-is, since they are
// only well-formed after per-iteration interpolation.
func validateURL(raw string, schemes ...string) error {
	if strings.Contains(raw, "{{") {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("plan: invalid url %q: %w", raw, err)
	}
	if u.Host == "" {
		return fmt.Errorf("plan: url %q must include a host", raw)
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return nil
		}
	}
	return fmt.Errorf("plan: url %q scheme must be one of %v, got %q", raw, schemes, u.Scheme)
}

// validateHostPort checks a gRPC dial target of the form host:port. Targets
// carrying a {{template}} placeholder are accepted as-is.
func validateHostPort(target string) error {
	if strings.Contains(target, "{{") {
		return nil
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("plan: grpc.target %q must be host:port: %w", target, err)
	}
	if host == "" || port == "" {
		return fmt.Errorf("plan: grpc.target %q must include host and port", target)
	}
	return nil
}

// ThinkTime returns the configured per-iteration pause.
func (p *Plan) ThinkTime() time.Duration {
	return time.Duration(p.ThinkTimeMs) * time.Millisecond
}
