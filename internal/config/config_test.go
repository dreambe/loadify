package config

import "testing"

func TestEnvIntInvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("LOADIFY_TEST_INT", "not-a-number")
	if got := EnvInt("LOADIFY_TEST_INT", 7); got != 7 {
		t.Errorf("EnvInt with invalid value = %d, want default 7", got)
	}
	t.Setenv("LOADIFY_TEST_INT", "42")
	if got := EnvInt("LOADIFY_TEST_INT", 7); got != 42 {
		t.Errorf("EnvInt = %d, want 42", got)
	}
	if got := EnvInt("LOADIFY_TEST_INT_UNSET", 3); got != 3 {
		t.Errorf("EnvInt unset = %d, want default 3", got)
	}
}

func TestAPIServerValidate(t *testing.T) {
	// Dev with the insecure default is allowed.
	dev := APIServer{Env: "dev", JWTSecret: insecureJWTSecret}
	if err := dev.Validate(); err != nil {
		t.Errorf("dev with default secret should be allowed: %v", err)
	}
	// Production with the insecure default is refused.
	prod := APIServer{Env: "prod", JWTSecret: insecureJWTSecret}
	if err := prod.Validate(); err == nil {
		t.Error("prod with default JWT secret must fail validation")
	}
	// Production with a real secret is fine.
	ok := APIServer{Env: "production", JWTSecret: "a-real-secret"}
	if err := ok.Validate(); err != nil {
		t.Errorf("prod with custom secret should pass: %v", err)
	}
}

func TestLoadAPIServerDefaults(t *testing.T) {
	cfg := LoadAPIServer()
	if cfg.Env != "dev" {
		t.Errorf("default env = %q, want dev", cfg.Env)
	}
	if cfg.IsProd() {
		t.Error("default env should not be production")
	}
}
