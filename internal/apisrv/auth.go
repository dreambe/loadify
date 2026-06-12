package apisrv

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

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
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

func userView(u *postgres.User) publicUserView {
	v := publicUserView{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Disabled: u.Disabled, LastLoginAt: u.LastLoginAt}
	if !u.CreatedAt.IsZero() {
		ca := u.CreatedAt
		v.CreatedAt = &ca
	}
	return v
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
	if u.Disabled {
		writeErr(w, http.StatusForbidden, "account disabled")
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
	if u.Disabled {
		writeErr(w, http.StatusForbidden, "account disabled")
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
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
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

// updateUserReq carries the admin-editable account fields; absent fields are
// left untouched.
type updateUserReq struct {
	Role     *string `json:"role,omitempty"`
	Password *string `json:"password,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

// handleUpdateUser lets an admin change a user's role, reset their password or
// enable/disable the account. Admins cannot demote, disable or lock themselves
// out, so an instance always keeps at least the acting admin.
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req updateUserReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := chi.URLParam(r, "id")
	c, _ := auth.FromContext(r.Context())
	if c != nil && c.Subject == id {
		if req.Disabled != nil && *req.Disabled {
			writeErr(w, http.StatusBadRequest, "cannot disable your own account")
			return
		}
		if req.Role != nil && auth.Role(*req.Role) != auth.RoleAdmin {
			writeErr(w, http.StatusBadRequest, "cannot demote your own account")
			return
		}
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	if req.Role != nil {
		if !auth.Role(*req.Role).Valid() {
			writeErr(w, http.StatusBadRequest, "invalid role")
			return
		}
		if err := s.pg.UpdateUserRole(ctx, id, *req.Role); err != nil {
			writeErr(w, statusForUserErr(err), err.Error())
			return
		}
	}
	if req.Password != nil {
		if len(*req.Password) < 8 {
			writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
			return
		}
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "hash error")
			return
		}
		if err := s.pg.SetUserPassword(ctx, id, hash); err != nil {
			writeErr(w, statusForUserErr(err), err.Error())
			return
		}
	}
	if req.Disabled != nil {
		if err := s.pg.SetUserDisabled(ctx, id, *req.Disabled); err != nil {
			writeErr(w, statusForUserErr(err), err.Error())
			return
		}
	}
	u, err := s.pg.GetUserByID(ctx, id)
	if err != nil {
		writeErr(w, statusForUserErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userView(u))
}

// handleDeleteUser removes an account (admin only, never your own).
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if c, ok := auth.FromContext(r.Context()); ok && c.Subject == id {
		writeErr(w, http.StatusBadRequest, "cannot delete your own account")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	if err := s.pg.DeleteUser(ctx, id); err != nil {
		writeErr(w, statusForUserErr(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// handleChangePassword lets any signed-in user rotate their own password after
// proving the current one (accounts without a password — Feishu-only — just
// set one).
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	c, ok := auth.FromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req changePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.NewPassword) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()
	u, err := s.pg.GetUserByID(ctx, c.Subject)
	if err != nil {
		writeErr(w, statusForUserErr(err), err.Error())
		return
	}
	if u.PasswordHash != "" && !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		writeErr(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash error")
		return
	}
	if err := s.pg.SetUserPassword(ctx, c.Subject, hash); err != nil {
		writeErr(w, statusForUserErr(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func statusForUserErr(err error) int {
	if errors.Is(err, postgres.ErrUserNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
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
