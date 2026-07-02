package apisrv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/dreambe/loadify/internal/auth"
)

// apiTokenPrefix marks loadify personal API tokens so they're recognizable in
// logs/config and distinct from JWT session tokens.
const apiTokenPrefix = "lfy_"

// hashAPIToken is what we persist: only the SHA-256 of the token is stored, so a
// database read never yields a usable credential. The raw token is shown to the
// user exactly once, at generation/reset.
func hashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// handleGetAPIToken reports whether the caller has a persistent CLI/agent token
// (used by loadifyctl and loadify-mcp via LOADIFY_TOKEN). Only a SHA-256 of the
// token is stored, so the raw value can't be shown again here — it is returned
// exactly once, at generation/reset (handleResetAPIToken). The token never
// expires and is invalidated only by reset or by disabling the account.
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
	writeJSON(w, http.StatusOK, map[string]any{"exists": u.APIToken != ""})
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
	// Persist only the hash; the raw token is returned to the caller once.
	if err := s.pg.SetUserAPIToken(ctx, userID, hashAPIToken(tok)); err != nil {
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
	u, err := s.pg.GetUserByAPIToken(ctx, hashAPIToken(raw))
	if err != nil || u == nil || u.Disabled {
		return nil, false
	}
	// Issued:0 is explicit: it makes validateClaims skip the creds-changed check
	// so the token survives password/role changes (it's its own credential),
	// while account-disable still revokes it.
	return &auth.Claims{Subject: u.ID, Email: u.Email, Name: u.Name, Role: auth.Role(u.Role), Issued: 0}, true
}

// newAPIToken returns a random, URL-safe opaque token with the loadify prefix.
func newAPIToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return apiTokenPrefix + hex.EncodeToString(b), nil
}
