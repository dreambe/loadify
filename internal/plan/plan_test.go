package plan

import (
	"strings"
	"testing"
)

func TestParseRejectsOversizedBody(t *testing.T) {
	big := strings.Repeat("x", DefaultMaxRequestBody+1)
	if _, err := Parse([]byte(`{"protocol":"http","http":{"url":"http://x/","body":"` + big + `"}}`)); err == nil {
		t.Error("expected error for body over the default limit")
	}
	// Within limit is fine.
	ok := strings.Repeat("x", 1024)
	if _, err := Parse([]byte(`{"protocol":"http","http":{"url":"http://x/","body":"` + ok + `"}}`)); err != nil {
		t.Errorf("unexpected error for small body: %v", err)
	}
}

func TestScriptTimeoutDefault(t *testing.T) {
	p, _ := Parse([]byte(`{"protocol":"script"}`))
	if p.ScriptTimeout() != DefaultScriptTimeout {
		t.Errorf("default script timeout = %v, want %v", p.ScriptTimeout(), DefaultScriptTimeout)
	}
	p2, _ := Parse([]byte(`{"protocol":"script","script_timeout_ms":500}`))
	if p2.ScriptTimeout().Milliseconds() != 500 {
		t.Errorf("override script timeout = %v, want 500ms", p2.ScriptTimeout())
	}
}

func TestParseHTTP(t *testing.T) {
	p, err := Parse([]byte(`{"protocol":"http","http":{"url":"http://x/"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.HTTP.Method != "GET" {
		t.Errorf("method default = %q, want GET", p.HTTP.Method)
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{
		`{"protocol":"http"}`,                       // missing http config
		`{"protocol":"http","http":{}}`,             // missing url
		`{"protocol":"bogus"}`,                      // unknown protocol
		`{"protocol":"grpc","grpc":{"target":"x"}}`, // missing full_method
		`not json`,
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c)); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestParseGRPCValid(t *testing.T) {
	_, err := Parse([]byte(`{"protocol":"grpc","grpc":{"target":"x:9","full_method":"/p.S/M"}}`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateNumericBounds(t *testing.T) {
	bad := []string{
		`{"protocol":"http","http":{"url":"http://x/"},"think_time_ms":-1}`,
		`{"protocol":"http","http":{"url":"http://x/"},"max_vus":-5}`,
		`{"protocol":"http","http":{"url":"http://x/"},"script_timeout_ms":-1}`,
		`{"protocol":"http","http":{"url":"http://x/"},"max_request_body_bytes":-1}`,
		`{"protocol":"http","http":{"url":"http://x/"},"think_time":{"distribution":"uniform","min_ms":100,"max_ms":10}}`,
		`{"protocol":"http","http":{"url":"http://x/"},"rendezvous":{"vus":-2}}`,
		`{"protocol":"http","http":{"url":"http://x/"},"auto_stop":{"error_rate_pct":150}}`,
		`{"protocol":"http","http":{"url":"http://x/"},"auto_stop":{"window_sec":-1}}`,
		`{"protocol":"http","http":{"url":"http://x/"},"alert":{"error_rate_pct":-1}}`,
		`{"protocol":"http","http":{"url":"http://x/","expect_status":700}}`,
		`{"protocol":"grpc","grpc":{"target":"x:9","full_method":"/p.S/M","max_messages":-1}}`,
	}
	for _, c := range bad {
		if _, err := Parse([]byte(c)); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
	good := []string{
		`{"protocol":"http","http":{"url":"http://x/"},"think_time_ms":10,"max_vus":5}`,
		`{"protocol":"http","http":{"url":"http://x/"},"auto_stop":{"error_rate_pct":50,"window_sec":10,"min_requests":20}}`,
		`{"protocol":"http","http":{"url":"http://x/","expect_status":204}}`,
		`{"protocol":"http","http":{"url":"http://x/"},"think_time":{"distribution":"uniform","min_ms":10,"max_ms":100}}`,
	}
	for _, c := range good {
		if _, err := Parse([]byte(c)); err != nil {
			t.Errorf("unexpected error for %q: %v", c, err)
		}
	}
}

func TestValidateURL(t *testing.T) {
	bad := []string{
		`{"protocol":"http","http":{"url":"/relative/path"}}`,                   // no host/scheme
		`{"protocol":"http","http":{"url":"ftp://host/x"}}`,                     // wrong scheme
		`{"protocol":"websocket","websocket":{"url":"notaurl"}}`,                // no host
		`{"protocol":"sse","sse":{"url":"ftp://h/x"}}`,                          // wrong scheme
		`{"protocol":"grpc","grpc":{"target":"noport","full_method":"/p.S/M"}}`, // not host:port
	}
	for _, c := range bad {
		if _, err := Parse([]byte(c)); err == nil {
			t.Errorf("expected url error for %q", c)
		}
	}
	good := []string{
		`{"protocol":"http","http":{"url":"http://host:8080/x"}}`,
		`{"protocol":"https","http":{"url":"https://host/x"}}`,
		`{"protocol":"websocket","websocket":{"url":"wss://host/ws"}}`,
		`{"protocol":"websocket","websocket":{"url":"http://host/ws"}}`,
		`{"protocol":"sse","sse":{"url":"https://host/stream"}}`,
		// Templated targets are accepted as-is (resolved at run time).
		`{"protocol":"http","http":{"url":"{{base}}/x"}}`,
		`{"protocol":"grpc","grpc":{"target":"{{host}}","full_method":"/p.S/M"}}`,
	}
	for _, c := range good {
		if _, err := Parse([]byte(c)); err != nil {
			t.Errorf("unexpected url error for %q: %v", c, err)
		}
	}
}

func TestHTTPAssertStatusOp(t *testing.T) {
	// contains/exists are meaningless against a numeric status code.
	bad := `{"protocol":"http","http":{"url":"http://x/","asserts":[{"source":"status","op":"contains","value":"2"}]}}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("expected error for status assert with contains op")
	}
	good := `{"protocol":"http","http":{"url":"http://x/","asserts":[{"source":"status","op":"eq","value":"200"}]}}`
	if _, err := Parse([]byte(good)); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestScenarioStepURLValidated(t *testing.T) {
	bad := `{"protocol":"scenario","scenario":{"mode":"sequence","steps":[{"method":"GET","url":"/nohost"}]}}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("expected error for scenario step with hostless url")
	}
	good := `{"protocol":"scenario","scenario":{"mode":"sequence","steps":[{"method":"GET","url":"https://h/a"},{"method":"GET","url":"{{base}}/b"}]}}`
	if _, err := Parse([]byte(good)); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
