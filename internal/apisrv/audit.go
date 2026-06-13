package apisrv

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/store/postgres"
)

// auditRecorder captures the response status so the audit middleware can record
// the outcome of a mutating request.
type auditRecorder struct {
	http.ResponseWriter
	status int
}

func (a *auditRecorder) WriteHeader(code int) {
	a.status = code
	a.ResponseWriter.WriteHeader(code)
}

func (a *auditRecorder) Write(b []byte) (int, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	return a.ResponseWriter.Write(b)
}

// auditMiddleware records mutating actions (who, when, what, outcome) to the
// audit log. Reads are not recorded — only POST/PUT/PATCH/DELETE. The write is
// best-effort and asynchronous so the request path is never blocked by it.
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		rec := &auditRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		// Login is a mutation but carries no authenticated identity yet and may
		// contain credentials in the body — skip it.
		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			return
		}
		entry := postgres.AuditEntry{
			Method: r.Method,
			Path:   r.URL.Path,
			Status: rec.status,
		}
		if c := s.claimsFromRequest(r); c != nil {
			id := c.Subject
			entry.UserID = &id
			entry.UserName = c.Name
			if entry.UserName == "" {
				entry.UserName = c.Email
			}
		}
		// Detached context: the request context is canceled once we return.
		go func() {
			ctx, cancel := withTimeout(context.Background())
			defer cancel()
			_ = s.pg.WriteAudit(ctx, entry)
		}()
	})
}

// claimsFromRequest verifies the bearer token (Authorization header or ?token=)
// and returns its claims, or nil. It mirrors the auth middleware's extraction so
// the audit layer can attribute actions without depending on context plumbing.
func (s *Server) claimsFromRequest(r *http.Request) *auth.Claims {
	h := r.Header.Get("Authorization")
	if h == "" {
		if t := r.URL.Query().Get("token"); t != "" {
			h = "Bearer " + t
		}
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return nil
	}
	c, err := auth.Parse(strings.TrimPrefix(h, prefix), s.jwtSecret)
	if err != nil {
		return nil
	}
	return c
}

// handleListAudit returns recent audit entries (admin only).
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	entries, err := s.pg.ListAudit(ctx, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []postgres.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
