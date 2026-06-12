// Package plan defines the declarative test-plan model that is authored in the
// UI/JSON, validated, and compiled into an executable run for the workers.
package plan

import (
	"encoding/json"
	"fmt"
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
)

// Plan is the top-level test definition.
type Plan struct {
	Protocol Protocol    `json:"protocol"`
	Name     string      `json:"name,omitempty"`
	HTTP     *HTTPConfig `json:"http,omitempty"`
	GRPC     *GRPCConfig `json:"grpc,omitempty"`
	WS       *WSConfig   `json:"websocket,omitempty"`
	SSE      *SSEConfig  `json:"sse,omitempty"`
	// ThinkTimeMs is the per-iteration pause applied after each request.
	ThinkTimeMs int64 `json:"think_time_ms,omitempty"`
	// MaxVUs caps the worker pool for the open (arrival-rate) model. 0 lets the
	// worker derive a safe bound from the peak target rate.
	MaxVUs int `json:"max_vus,omitempty"`
}

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
	switch p.Protocol {
	case HTTP, HTTPS:
		if p.HTTP == nil {
			return fmt.Errorf("plan: http config required for protocol %q", p.Protocol)
		}
		if p.HTTP.URL == "" {
			return fmt.Errorf("plan: http.url is required")
		}
		if p.HTTP.Method == "" {
			p.HTTP.Method = "GET"
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
	case WebSocket:
		if p.WS == nil || p.WS.URL == "" {
			return fmt.Errorf("plan: websocket.url is required")
		}
	case SSE:
		if p.SSE == nil || p.SSE.URL == "" {
			return fmt.Errorf("plan: sse.url is required")
		}
	case Script:
		// A script plan carries no protocol target; the script generates traffic.
	default:
		return fmt.Errorf("plan: unknown protocol %q", p.Protocol)
	}
	return nil
}

// ThinkTime returns the configured per-iteration pause.
func (p *Plan) ThinkTime() time.Duration {
	return time.Duration(p.ThinkTimeMs) * time.Millisecond
}
