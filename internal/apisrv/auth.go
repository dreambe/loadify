package apisrv

import (
	"encoding/json"
	"errors"
	"net/http"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/store/postgres"
	"github.com/go-chi/chi/v5"
)

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResp struct {
	Token string         `json:"token"`
	User  publicUserView `json:"user"`
}

type publicUserView struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

func userView(u *postgres.User) publicUserView {
	return publicUserView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role}
}

// handleLogin authenticates an email/password user and returns a JWT.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	u, err := s.pg.GetUserByEmail(ctx, req.Email)
	if err != nil || u.PasswordHash == "" || !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.issueToken(w, u)
	_ = s.pg.TouchLogin(ctx, u.ID)
}

// handleFeishuLogin redirects the browser to Feishu's authorization page.
func (s *Server) handleFeishuLogin(w http.ResponseWriter, r *http.Request) {
	if !s.feishu.Enabled() {
		writeErr(w, http.StatusNotImplemented, "feishu login not configured")
		return
	}
	state := r.URL.Query().Get("state")
	http.Redirect(w, r, s.feishu.AuthCodeURL(state), http.StatusFound)
}

// handleFeishuCallback exchanges the code, upserts the user and redirects back
// to the frontend with a freshly issued token.
func (s *Server) handleFeishuCallback(w http.ResponseWriter, r *http.Request) {
	if !s.feishu.Enabled() {
		writeErr(w, http.StatusNotImplemented, "feishu login not configured")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "missing code")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	fu, err := s.feishu.Exchange(ctx, code)
	if err != nil {
		s.log.Warn("feishu exchange failed", "err", err)
		writeErr(w, http.StatusBadGateway, "feishu exchange failed")
		return
	}
	u, err := s.pg.UpsertFeishuUser(ctx, fu.OpenID, fu.Email, fu.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	token, err := s.mintToken(u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token error")
		return
	}
	// Hand the token to the SPA via a fragment so it never hits server logs.
	http.Redirect(w, r, s.frontendURL+"/login#token="+token, http.StatusFound)
}

// handleMe returns the caller's claims.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.FromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, publicUserView{ID: c.Subject, Email: c.Email, Name: c.Name, Role: string(c.Role)})
}

// --- user management (admin) ---

type createUserReq struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	role := auth.Role(req.Role)
	if !role.Valid() {
		writeErr(w, http.StatusBadRequest, "invalid role")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "email and password required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash error")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	u, err := s.pg.CreateUser(ctx, req.Email, req.Name, string(role), hash)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, userView(u))
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	users, err := s.pg.ListUsers(ctx, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]publicUserView, 0, len(users))
	for i := range users {
		out = append(out, userView(&users[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleStopRun signals the coordinator to stop a run.
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	runID := chi.URLParam(r, "id")
	if _, err := s.coord.StopRun(ctx, &loadifyv1.StopRunRequest{RunId: runID, Graceful: true}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID, "status": "stopping"})
}

// issueToken writes a token response for the user.
func (s *Server) issueToken(w http.ResponseWriter, u *postgres.User) {
	token, err := s.mintToken(u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token error")
		return
	}
	writeJSON(w, http.StatusOK, tokenResp{Token: token, User: userView(u)})
}

func (s *Server) mintToken(u *postgres.User) (string, error) {
	if s.jwtSecret == "" {
		return "", errors.New("jwt secret not configured")
	}
	return auth.Issue(auth.Claims{
		Subject: u.ID,
		Email:   u.Email,
		Name:    u.Name,
		Role:    auth.Role(u.Role),
	}, s.jwtSecret, s.jwtTTL)
}
