// Package importer converts external request formats (curl, HAR, Postman,
// OpenAPI) into a Loadify test draft the builder can prefill. Multi-request
// sources become a sequence scenario; a single request becomes an HTTP test.
package importer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dreambe/loadify/internal/plan"
)

// Draft is an un-saved test the import endpoint returns for the user to review.
type Draft struct {
	Name     string          `json:"name"`
	Protocol string          `json:"protocol"`
	Plan     json.RawMessage `json:"plan"`
}

// req is one parsed HTTP request, shared by all format parsers.
type req struct {
	Name    string
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

// Parse dispatches to the parser for format ("curl"|"har"|"postman"|"openapi").
func Parse(format, content string) (*Draft, error) {
	var (
		reqs []req
		err  error
	)
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "curl":
		reqs, err = parseCurl(content)
	case "har":
		reqs, err = parseHAR([]byte(content))
	case "postman":
		reqs, err = parsePostman([]byte(content))
	case "openapi", "swagger":
		reqs, err = parseOpenAPI([]byte(content))
	default:
		return nil, fmt.Errorf("importer: unknown format %q", format)
	}
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, fmt.Errorf("importer: no requests found in %s input", format)
	}
	return toDraft(format, reqs)
}

// toDraft builds a single HTTP plan for one request, or a sequence scenario for
// several, and validates the result through plan.Parse.
func toDraft(format string, reqs []req) (*Draft, error) {
	d := &Draft{Name: "imported-" + format}
	if len(reqs) == 1 {
		r := reqs[0]
		d.Protocol = "http"
		p := map[string]any{"protocol": "http", "http": httpObj(r)}
		d.Plan, _ = json.Marshal(p)
		if r.Name != "" {
			d.Name = r.Name
		}
	} else {
		d.Protocol = "scenario"
		steps := make([]map[string]any, 0, len(reqs))
		for i, r := range reqs {
			step := httpObj(r)
			name := r.Name
			if name == "" {
				name = fmt.Sprintf("step%d", i+1)
			}
			step["name"] = name
			steps = append(steps, step)
		}
		p := map[string]any{"protocol": "scenario", "scenario": map[string]any{"mode": "sequence", "steps": steps}}
		d.Plan, _ = json.Marshal(p)
	}
	// Validate the produced plan so the builder always loads something runnable.
	if _, err := plan.Parse(d.Plan); err != nil {
		return nil, fmt.Errorf("importer: produced an invalid plan: %w", err)
	}
	return d, nil
}

func httpObj(r req) map[string]any {
	o := map[string]any{"method": orGET(r.Method), "url": r.URL}
	if len(r.Headers) > 0 {
		o["headers"] = r.Headers
	}
	if r.Body != "" {
		o["body"] = r.Body
	}
	return o
}

func orGET(m string) string {
	if m == "" {
		return "GET"
	}
	return strings.ToUpper(m)
}
