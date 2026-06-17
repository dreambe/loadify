package apisrv

import (
	"net/http"
	"time"

	"github.com/dreambe/loadify/internal/auth"
)

// apiTokenTTL is how long a generated CLI/agent token stays valid. Long-lived
// (a personal access token) but still bounded; revocable by disabling the
// account or rotating credentials (see validateClaims).
const apiTokenTTL = 365 * 24 * time.Hour

// handleCreateAPIToken mints a long-lived token carrying the caller's identity,
// for use by the CLI (loadifyctl) and AI agents (loadify-mcp) via
// LOADIFY_TOKEN — so an agent can create tests and run load tests on the
// user's behalf. Shown once; the user copies and stores it.
func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.FromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	tok, err := auth.Issue(auth.Claims{Subject: c.Subject, Email: c.Email, Name: c.Name, Role: c.Role}, s.jwtSecret, apiTokenTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"expires_at": time.Now().Add(apiTokenTTL).UTC().Format(time.RFC3339),
	})
}
