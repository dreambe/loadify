package plan

import "testing"

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
