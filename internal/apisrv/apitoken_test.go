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

// TestPersistentAPIToken covers the Feishu-style token lifecycle: GET mints &
// returns a stable token, repeated GET returns the same value, POST rotates it,
// and the opaque token authenticates as a bearer (the JWT-fallback resolver).
func TestPersistentAPIToken(t *testing.T) {
	meta := newFakeMeta()
	meta.usersByID = map[string]*postgres.User{"u": {ID: "u", Email: "u@x", Name: "U", Role: "operator"}}
	srv := newTestServer(meta, &fakeCoord{})
	h := srv.Handler()

	getTok := func(method string) string {
		req := httptest.NewRequest(method, "/api/v1/auth/token", nil)
		req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleOperator))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s token: got %d, body %s", method, rr.Code, rr.Body.String())
		}
		var out struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Token
	}

	first := getTok("GET")
	if !strings.HasPrefix(first, apiTokenPrefix) {
		t.Fatalf("token missing prefix: %q", first)
	}
	if again := getTok("GET"); again != first {
		t.Fatalf("GET not stable: %q != %q", first, again)
	}

	// The opaque token authenticates a real request via the bearer fallback.
	req := httptest.NewRequest("GET", "/api/v1/tests", nil)
	req.Header.Set("Authorization", "Bearer "+first)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("auth with API token: got %d want 200", rr.Code)
	}

	// Reset rotates the value and invalidates the old one.
	rotated := getTok("POST")
	if rotated == first {
		t.Fatal("POST did not rotate token")
	}
	req = httptest.NewRequest("GET", "/api/v1/tests", nil)
	req.Header.Set("Authorization", "Bearer "+first)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("old token after reset: got %d want 401", rr.Code)
	}
}
