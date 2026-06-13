package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Role is a coarse permission level. Ordering (viewer < operator < admin) drives
// RequireRole checks.
type Role string

const (
	RoleViewer   Role = "viewer"   // read-only: list/inspect tests, runs, metrics
	RoleOperator Role = "operator" // viewer + create tests and start/stop runs
	RoleAdmin    Role = "admin"    // operator + user management
)

var roleRank = map[Role]int{RoleViewer: 1, RoleOperator: 2, RoleAdmin: 3}

// AtLeast reports whether r meets or exceeds min.
func (r Role) AtLeast(min Role) bool { return roleRank[r] >= roleRank[min] }

// Valid reports whether r is a known role.
func (r Role) Valid() bool { _, ok := roleRank[r]; return ok }

type ctxKey int

const claimsKey ctxKey = 0

// Middleware verifies bearer tokens and enforces role minimums.
type Middleware struct {
	Secret string
	// Validate, when set, is consulted after signature/expiry checks pass. It
	// returns false to reject an otherwise-valid token — used to honor account
	// disable / credential-change revocation that JWT expiry alone cannot.
	Validate func(*Claims) bool
}

// claimsFrom extracts and verifies the bearer token from the request.
func (m Middleware) claimsFrom(r *http.Request) (*Claims, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		// Allow token via query param for the WebSocket handshake, where custom
		// headers cannot be set by browsers.
		if t := r.URL.Query().Get("token"); t != "" {
			h = "Bearer " + t
		}
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return nil, false
	}
	c, err := Parse(strings.TrimPrefix(h, prefix), m.Secret)
	if err != nil {
		return nil, false
	}
	return c, true
}

// Require returns middleware that rejects requests lacking a valid token whose
// role meets min.
func (m Middleware) Require(min Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := m.claimsFrom(r)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			if m.Validate != nil && !m.Validate(c) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token revoked"})
				return
			}
			if !c.Role.AtLeast(min) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), c)))
		})
	}
}

// WithClaims stores claims in the context.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

// FromContext returns the claims set by Require, if any.
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
