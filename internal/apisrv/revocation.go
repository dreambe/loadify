package apisrv

import (
	"context"
	"time"

	"github.com/dreambe/loadify/internal/auth"
)

// revCacheTTL bounds how long an account's revocation state is cached, so a
// disable / password-reset / role-change takes effect within this window
// without a database read on every authenticated request.
const revCacheTTL = 30 * time.Second

// authState is the cached revocation-relevant snapshot of an account.
type authState struct {
	disabled       bool
	credsChangedAt int64 // unix seconds
	fetched        time.Time
}

// validateClaims rejects otherwise-valid tokens for disabled accounts or tokens
// issued before the account's credentials last changed. Failures to look up the
// user (e.g. deleted account) are treated as revoked.
func (s *Server) validateClaims(c *auth.Claims) bool {
	if c == nil || c.Subject == "" {
		return false
	}
	st, ok := s.cachedAuthState(c.Subject)
	if !ok {
		return false
	}
	if st.disabled {
		return false
	}
	// A token is stale if it was issued before the last credential change.
	if c.Issued != 0 && c.Issued < st.credsChangedAt {
		return false
	}
	return true
}

func (s *Server) cachedAuthState(userID string) (authState, bool) {
	now := time.Now()
	if v, ok := s.revCache.Load(userID); ok {
		st := v.(authState)
		if now.Sub(st.fetched) < revCacheTTL {
			return st, true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := s.pg.GetUserByID(ctx, userID)
	if err != nil || u == nil {
		return authState{}, false
	}
	st := authState{disabled: u.Disabled, credsChangedAt: u.CredsChangedAt.Unix(), fetched: now}
	s.revCache.Store(userID, st)
	return st, true
}
