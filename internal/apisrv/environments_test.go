package apisrv

import "testing"

func TestSubstituteEnv(t *testing.T) {
	vars := map[string]string{"base_url": "https://prod.example.com", "token": "abc"}
	cases := []struct{ in, want string }{
		{`{"http":{"url":"{{base_url}}/login"}}`, `{"http":{"url":"https://prod.example.com/login"}}`},
		{`Bearer {{ token }}`, `Bearer abc`},
		// Unknown keys are preserved for the scenario runtime to resolve.
		{`{{userId}}/{{base_url}}`, `{{userId}}/https://prod.example.com`},
		{`no placeholders`, `no placeholders`},
	}
	for _, c := range cases {
		if got := substituteEnv(c.in, vars); got != c.want {
			t.Errorf("substituteEnv(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Empty vars is a no-op.
	if got := substituteEnv("{{base_url}}", nil); got != "{{base_url}}" {
		t.Errorf("nil vars should be a no-op, got %q", got)
	}
}
