package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dreambe/loadify/internal/auth"
)

func TestJWTRoundTrip(t *testing.T) {
	secret := "test-secret"
	tok, err := auth.Issue(auth.Claims{Subject: "u1", Email: "a@b.com", Role: auth.RoleOperator}, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := auth.Parse(tok, secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Subject != "u1" || c.Role != auth.RoleOperator {
		t.Errorf("claims mismatch: %+v", c)
	}
}

func TestJWTRejectsTamperAndWrongSecret(t *testing.T) {
	tok, _ := auth.Issue(auth.Claims{Subject: "u1", Role: auth.RoleAdmin}, "secret", time.Hour)
	if _, err := auth.Parse(tok, "other-secret"); err == nil {
		t.Error("expected failure with wrong secret")
	}
	if _, err := auth.Parse(tok+"x", "secret"); err == nil {
		t.Error("expected failure on tampered token")
	}
}

func TestJWTExpiry(t *testing.T) {
	tok, _ := auth.Issue(auth.Claims{Subject: "u1", Role: auth.RoleViewer}, "secret", -time.Minute)
	if _, err := auth.Parse(tok, "secret"); err == nil {
		t.Error("expected expired token to fail")
	}
}

func TestRoleAtLeast(t *testing.T) {
	if !auth.RoleAdmin.AtLeast(auth.RoleOperator) {
		t.Error("admin should outrank operator")
	}
	if auth.RoleViewer.AtLeast(auth.RoleOperator) {
		t.Error("viewer should not meet operator")
	}
}

func TestMiddlewareEnforcesRole(t *testing.T) {
	secret := "s"
	mw := auth.Middleware{Secret: secret}
	handler := mw.Require(auth.RoleOperator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := auth.FromContext(r.Context())
		w.Write([]byte(c.Subject))
	}))

	// No token -> 401.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d want 401", rr.Code)
	}

	// Viewer token -> 403.
	viewerTok, _ := auth.Issue(auth.Claims{Subject: "v", Role: auth.RoleViewer}, secret, time.Hour)
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+viewerTok)
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("viewer: got %d want 403", rr.Code)
	}

	// Operator token -> 200 and claims propagate.
	opTok, _ := auth.Issue(auth.Claims{Subject: "op", Role: auth.RoleOperator}, secret, time.Hour)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+opTok)
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "op" {
		t.Errorf("operator: got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestMiddlewareAcceptsQueryToken(t *testing.T) {
	secret := "s"
	mw := auth.Middleware{Secret: secret}
	handler := mw.Require(auth.RoleViewer)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	tok, _ := auth.Issue(auth.Claims{Subject: "v", Role: auth.RoleViewer}, secret, time.Hour)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/ws?token="+tok, nil))
	if rr.Code != http.StatusOK {
		t.Errorf("query token: got %d want 200", rr.Code)
	}
}

func TestPasswordHashing(t *testing.T) {
	h, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(h, "hunter2") {
		t.Error("correct password should verify")
	}
	if auth.CheckPassword(h, "wrong") {
		t.Error("wrong password should not verify")
	}
}

func TestFeishuExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/app_access_token/internal"):
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "app_access_token": "app-tok"})
		case strings.HasSuffix(r.URL.Path, "/oidc/access_token"):
			if r.Header.Get("Authorization") != "Bearer app-tok" {
				t.Errorf("missing app token bearer, got %q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"access_token": "user-tok"}})
		case strings.HasSuffix(r.URL.Path, "/user_info"):
			if r.Header.Get("Authorization") != "Bearer user-tok" {
				t.Errorf("missing user token bearer, got %q", r.Header.Get("Authorization"))
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"open_id": "ou_123", "name": "Shark", "email": "shark@example.com",
			}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := &auth.FeishuClient{AppID: "cli", AppSecret: "sec", BaseURL: srv.URL, HTTP: srv.Client()}
	if !c.Enabled() {
		t.Fatal("client should be enabled")
	}
	u, err := c.Exchange(context.Background(), "auth-code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if u.OpenID != "ou_123" || u.Email != "shark@example.com" || u.Name != "Shark" {
		t.Errorf("unexpected user: %+v", u)
	}
}

func TestFeishuAuthCodeURL(t *testing.T) {
	c := &auth.FeishuClient{AppID: "cli", AppSecret: "sec", RedirectURL: "https://app/cb", BaseURL: "https://feishu.test"}
	got := c.AuthCodeURL("xyz")
	for _, want := range []string{"app_id=cli", "redirect_uri=https%3A%2F%2Fapp%2Fcb", "state=xyz", "/open-apis/authen/v1/index"} {
		if !strings.Contains(got, want) {
			t.Errorf("auth url %q missing %q", got, want)
		}
	}
}
