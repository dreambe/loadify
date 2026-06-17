package apisrv

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimiterCapsAttempts(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if rl.allow("1.2.3.4") {
		t.Fatal("4th attempt should be blocked")
	}
	// A different key has its own budget.
	if !rl.allow("5.6.7.8") {
		t.Fatal("other client should be allowed")
	}
}

func TestRateLimiterWindowResets(t *testing.T) {
	rl := newRateLimiter(1, 10*time.Millisecond)
	if !rl.allow("k") {
		t.Fatal("first allowed")
	}
	if rl.allow("k") {
		t.Fatal("second blocked within window")
	}
	time.Sleep(15 * time.Millisecond)
	if !rl.allow("k") {
		t.Fatal("allowed again after window")
	}
}

func TestClientIP(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:5555"
	if got := clientIP(r); got != "10.0.0.5" {
		t.Errorf("RemoteAddr: got %q want 10.0.0.5", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("XFF: got %q want 203.0.113.9", got)
	}
}
