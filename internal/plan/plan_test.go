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
