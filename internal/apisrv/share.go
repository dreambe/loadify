package apisrv

import (
	"net/http"
	"time"

	"github.com/dreambe/loadify/internal/auth"
	"github.com/go-chi/chi/v5"
)

// runRead authorizes a run's read endpoints by either a normal session OR a
// public share token scoped to that run, so a shared link can drive the real
// (interactive) run page with no login.
func (s *Server) runRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := s.authmw.ClaimsFrom(r); ok {
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), c)))
			return
		}
		runID := chi.URLParam(r, "id")
		if share := r.URL.Query().Get("share"); share != "" {
			if c, err := auth.Parse(share, s.jwtSecret); err == nil && c.Subject == shareSubject(runID) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized")
	})
}

// shareTokenTTL bounds how long a public report share link stays valid.
const shareTokenTTL = 30 * 24 * time.Hour

// shareSubject is the JWT subject identifying a share token scoped to one run.
func shareSubject(runID string) string { return "share:" + runID }

// handleShareRun mints a signed, expiring public share token for a run's
// report, so the report URL can be sent to someone without a loadify account.
func (s *Server) handleShareRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	if _, err := s.pg.GetRun(ctx, runID); err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	tok, err := auth.Issue(auth.Claims{Subject: shareSubject(runID), Role: auth.RoleViewer}, s.jwtSecret, shareTokenTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"expires_at": time.Now().Add(shareTokenTTL).UTC().Format(time.RFC3339),
	})
}

// reportAuthorized allows a run's report when the request carries either a
// valid session token (any role) or a valid share token scoped to this run.
func (s *Server) reportAuthorized(r *http.Request, runID string) bool {
	if share := r.URL.Query().Get("share"); share != "" {
		if c, err := auth.Parse(share, s.jwtSecret); err == nil && c.Subject == shareSubject(runID) {
			return true
		}
	}
	_, ok := s.authmw.ClaimsFrom(r)
	return ok
}
