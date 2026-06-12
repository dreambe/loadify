package importer

import (
	"encoding/json"
	"testing"

	"github.com/dreambe/loadify/internal/plan"
)

func mustParse(t *testing.T, format, content string) *Draft {
	t.Helper()
	d, err := Parse(format, content)
	if err != nil {
		t.Fatalf("%s parse: %v", format, err)
	}
	if _, err := plan.Parse(d.Plan); err != nil {
		t.Fatalf("%s produced invalid plan: %v\n%s", format, err, d.Plan)
	}
	return d
}

func TestParseCurl(t *testing.T) {
	d := mustParse(t, "curl", `curl -X POST 'https://api.example.com/login' \
	  -H 'Content-Type: application/json' \
	  -H "Accept: application/json" \
	  -d '{"user":"alice","pass":"secret"}'`)
	if d.Protocol != "http" {
		t.Fatalf("protocol = %q, want http", d.Protocol)
	}
	var p struct {
		HTTP struct {
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		} `json:"http"`
	}
	_ = json.Unmarshal(d.Plan, &p)
	if p.HTTP.Method != "POST" || p.HTTP.URL != "https://api.example.com/login" {
		t.Errorf("method/url = %s %s", p.HTTP.Method, p.HTTP.URL)
	}
	if p.HTTP.Headers["Content-Type"] != "application/json" || p.HTTP.Headers["Accept"] != "application/json" {
		t.Errorf("headers = %+v", p.HTTP.Headers)
	}
	if p.HTTP.Body == "" {
		t.Error("body not captured")
	}
}

func TestParseCurlBareURLDefaultsGet(t *testing.T) {
	d := mustParse(t, "curl", `curl https://example.com/health`)
	if d.Protocol != "http" {
		t.Fatalf("protocol = %q", d.Protocol)
	}
}

func TestParseHARMultiBecomesScenario(t *testing.T) {
	har := `{"log":{"entries":[
	  {"request":{"method":"GET","url":"https://api/x","headers":[{"name":"Accept","value":"*/*"},{"name":":authority","value":"api"}]}},
	  {"request":{"method":"POST","url":"https://api/y","headers":[],"postData":{"text":"{}"}}}
	]}}`
	d := mustParse(t, "har", har)
	if d.Protocol != "scenario" {
		t.Fatalf("protocol = %q, want scenario", d.Protocol)
	}
	var p struct {
		Scenario struct {
			Mode  string `json:"mode"`
			Steps []struct {
				URL     string            `json:"url"`
				Headers map[string]string `json:"headers"`
			} `json:"steps"`
		} `json:"scenario"`
	}
	_ = json.Unmarshal(d.Plan, &p)
	if p.Scenario.Mode != "sequence" || len(p.Scenario.Steps) != 2 {
		t.Fatalf("scenario = %+v", p.Scenario)
	}
	// HTTP/2 pseudo-header should be dropped.
	if _, ok := p.Scenario.Steps[0].Headers[":authority"]; ok {
		t.Error("pseudo-header :authority should be skipped")
	}
}

func TestParsePostman(t *testing.T) {
	pm := `{"item":[
	  {"name":"Login","request":{"method":"POST","header":[{"key":"X-K","value":"v"}],"body":{"raw":"{}"},"url":{"raw":"https://api/login"}}},
	  {"name":"Folder","item":[{"name":"Me","request":{"method":"GET","url":"https://api/me"}}]}
	]}`
	d := mustParse(t, "postman", pm)
	if d.Protocol != "scenario" {
		t.Fatalf("protocol = %q, want scenario", d.Protocol)
	}
}

func TestParseOpenAPI(t *testing.T) {
	oas := `{"servers":[{"url":"https://api.example.com/v1"}],"paths":{
	  "/users":{"get":{},"post":{}},
	  "/health":{"get":{}}
	}}`
	d := mustParse(t, "openapi", oas)
	if d.Protocol != "scenario" {
		t.Fatalf("protocol = %q, want scenario", d.Protocol)
	}
	var p struct {
		Scenario struct {
			Steps []struct {
				URL string `json:"url"`
			} `json:"steps"`
		} `json:"scenario"`
	}
	_ = json.Unmarshal(d.Plan, &p)
	if len(p.Scenario.Steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(p.Scenario.Steps))
	}
	for _, s := range p.Scenario.Steps {
		if s.URL[:len("https://api.example.com/v1")] != "https://api.example.com/v1" {
			t.Errorf("step url missing base: %s", s.URL)
		}
	}
}

func TestParseUnknownFormat(t *testing.T) {
	if _, err := Parse("xml", "<x/>"); err == nil {
		t.Error("expected error for unknown format")
	}
}
