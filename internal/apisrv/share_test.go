package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dreambe/loadify/internal/auth"
)

// TestRunReportShareLink verifies the public report share token: an operator
// mints it, anyone can open the report with ?share= (no login), and the token
// is scoped to one run and required when no session is present.
func TestRunReportShareLink(t *testing.T) {
	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	h := srv.Handler()

	get := func(path, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// No session and no share token → unauthorized.
	if rr := get("/api/v1/runs/run-1/report.html", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauth report = %d, want 401", rr.Code)
	}

	// Operator mints a share token.
	req := httptest.NewRequest("POST", "/api/v1/runs/run-1/share", nil)
	req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleOperator))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("share mint = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var sh struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &sh); err != nil || sh.Token == "" {
		t.Fatalf("share resp decode: %v token=%q", err, sh.Token)
	}

	// The share token opens the report with no session at all.
	if rr := get("/api/v1/runs/run-1/report.html?share="+sh.Token, ""); rr.Code != http.StatusOK {
		t.Fatalf("share report = %d, want 200: %s", rr.Code, rr.Body.String())
	} else if ct := rr.Header().Get("Content-Type"); ct == "" {
		t.Errorf("report content-type empty")
	}

	// A token minted for run-1 must not open run-2's report.
	if rr := get("/api/v1/runs/run-2/report.html?share="+sh.Token, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("cross-run share = %d, want 401", rr.Code)
	}

	// The same token authorizes the run's read endpoints, so the shared link
	// can drive the real interactive page with no session.
	if rr := get("/api/v1/runs/run-1?share="+sh.Token, ""); rr.Code != http.StatusOK {
		t.Errorf("GET run via share = %d, want 200", rr.Code)
	}
	if rr := get("/api/v1/runs/run-1/series?share="+sh.Token, ""); rr.Code != http.StatusOK {
		t.Errorf("GET series via share = %d, want 200", rr.Code)
	}
	// No session and no share → the read endpoint is still locked.
	if rr := get("/api/v1/runs/run-1", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("GET run unauthenticated = %d, want 401", rr.Code)
	}

	// A viewer must not be able to mint a share token (operator+ only).
	req = httptest.NewRequest("POST", "/api/v1/runs/run-1/share", nil)
	req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleViewer))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer mint = %d, want 403", rr.Code)
	}
}
