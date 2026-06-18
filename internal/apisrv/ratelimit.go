package apisrv

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// loginRateLimit caps password-login attempts per client to slow credential
// stuffing / brute force. It's a small fixed-window counter per key (client IP):
// cheap, dependency-free, and self-pruning. Successful logins aren't refunded —
// the cap is on attempts within the window, which is what throttles guessing.
const (
	loginMaxAttempts = 10
	loginWindow      = time.Minute
)

type rateLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	buckets map[string]*rlBucket
}

type rlBucket struct {
	count int
	reset time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, buckets: map[string]*rlBucket{}}
}

// allow records an attempt for key and reports whether it's within the cap.
func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b := rl.buckets[key]
	if b == nil || now.After(b.reset) {
		// New or expired window. Opportunistically prune other stale buckets so
		// the map can't grow without bound under churning client IPs.
		if len(rl.buckets) > 4096 {
			for k, v := range rl.buckets {
				if now.After(v.reset) {
					delete(rl.buckets, k)
				}
			}
		}
		rl.buckets[key] = &rlBucket{count: 1, reset: now.Add(rl.window)}
		return true
	}
	b.count++
	return b.count <= rl.max
}

// clientIP extracts the best-effort client address for rate-limit keying. It
// trusts X-Forwarded-For's first hop (the deployment is expected behind a known
// proxy/ingress); otherwise it falls back to the connection's remote address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexByteComma(xff); i >= 0 {
			return trimSpace(xff[:i])
		}
		return trimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func indexByteComma(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
