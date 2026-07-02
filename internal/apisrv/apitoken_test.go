package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/store/postgres"
)

// TestPersistentAPIToken covers the GitHub-PAT-style token lifecycle: only a
// hash is stored, so GET never returns a raw token (just whether one exists),
// POST mints/rotates and returns the raw value once, the raw token
// authenticates as a bearer (JWT-fallback resolver), and reset invalidates the
// old one.
func TestPersistentAPIToken(t *testing.T) {
	meta := newFakeMeta()
	meta.usersByID = map[string]*postgres.User{"u": {ID: "u", Email: "u@x", Name: "U", Role: "operator"}}
	srv := newTestServer(meta, &fakeCoord{})
	h := srv.Handler()

	call := func(method string) map[string]any {
		req := httptest.NewRequest(method, "/api/v1/auth/token", nil)
		req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleOperator))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s token: got %d, body %s", method, rr.Code, rr.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	authCode := func(tok string) int {
		req := httptest.NewRequest("GET", "/api/v1/tests", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}

	// GET never returns a raw token; initially none exists.
	if m := call("GET"); m["exists"] != false || m["token"] != nil {
		t.Fatalf("expected no token initially: %v", m)
	}

	// POST mints and returns the raw token exactly once.
	minted, _ := call("POST")["token"].(string)
	if !strings.HasPrefix(minted, apiTokenPrefix) {
		t.Fatalf("minted token missing prefix: %q", minted)
	}

	// GET now reports existence but still withholds the raw token.
	if m := call("GET"); m["exists"] != true || m["token"] != nil {
		t.Fatalf("expected exists=true and no raw token: %v", m)
	}

	// The raw token authenticates via the bearer fallback (hash round-trips).
	if code := authCode(minted); code != http.StatusOK {
		t.Fatalf("auth with API token: got %d want 200", code)
	}

	// Reset rotates: a new raw token works, the old one stops.
	rotated, _ := call("POST")["token"].(string)
	if rotated == minted || rotated == "" {
		t.Fatalf("POST did not rotate token: %q -> %q", minted, rotated)
	}
	if code := authCode(minted); code != http.StatusUnauthorized {
		t.Fatalf("old token after reset: got %d want 401", code)
	}
	if code := authCode(rotated); code != http.StatusOK {
		t.Fatalf("rotated token: got %d want 200", code)
	}
}
