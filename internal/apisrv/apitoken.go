package apisrv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/dreambe/loadify/internal/auth"
)

// apiTokenPrefix marks loadify personal API tokens so they're recognizable in
// logs/config and distinct from JWT session tokens.
const apiTokenPrefix = "lfy_"

// handleGetAPIToken returns the caller's persistent CLI/agent token, minting one
// on first access. The token is permanent (Feishu-style app secret): it never
// expires, is viewable any time here, and is invalidated only by reset
// (handleResetAPIToken) or by disabling the account. Used by the CLI
// (loadifyctl) and AI agents (loadify-mcp) via LOADIFY_TOKEN so an agent can
// create tests and run load tests on the user's behalf.
func (s *Server) handleGetAPIToken(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.FromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	u, err := s.pg.GetUserByID(ctx, c.Subject)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tok := u.APIToken
	if tok == "" {
		// Lazily mint on first view so the panel always has a token to show.
		if tok, err = s.rotateAPIToken(ctx, c.Subject); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

// handleResetAPIToken generates a fresh persistent token, invalidating the old
// one. Mirrors "重置/reset" in a Feishu-style credentials panel.
func (s *Server) handleResetAPIToken(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.FromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	tok, err := s.rotateAPIToken(ctx, c.Subject)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok})
}

// rotateAPIToken generates a new opaque token, persists it for the user and
// returns it.
func (s *Server) rotateAPIToken(ctx context.Context, userID string) (string, error) {
	tok, err := newAPIToken()
	if err != nil {
		return "", err
	}
	if err := s.pg.SetUserAPIToken(ctx, userID, tok); err != nil {
		return "", err
	}
	return tok, nil
}

// resolveAPIToken maps a persistent opaque token to its owner's claims. Wired as
// auth.Middleware.Resolve so a bearer that is not a valid JWT is checked against
// stored API tokens. Issued is left 0 so the token survives password changes
// (it's its own credential); account disable still revokes it via validateClaims.
func (s *Server) resolveAPIToken(raw string) (*auth.Claims, bool) {
	if len(raw) < len(apiTokenPrefix) || raw[:len(apiTokenPrefix)] != apiTokenPrefix {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := s.pg.GetUserByAPIToken(ctx, raw)
	if err != nil || u == nil || u.Disabled {
		return nil, false
	}
	return &auth.Claims{Subject: u.ID, Email: u.Email, Name: u.Name, Role: auth.Role(u.Role)}, true
}

// newAPIToken returns a random, URL-safe opaque token with the loadify prefix.
func newAPIToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return apiTokenPrefix + hex.EncodeToString(b), nil
}
