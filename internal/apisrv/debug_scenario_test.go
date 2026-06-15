package apisrv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dreambe/loadify/internal/auth"
)

// TestDebugScenarioResolvesChain verifies that debugging step 2 runs step 1
// first, extracts its variable and substitutes it — so the dependent step hits
// the resolved URL (200) rather than firing the literal {{var}} template (404).
func TestDebugScenarioResolvesChain(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			_, _ = w.Write([]byte(`{"uid":7}`))
		case "/b/7":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer target.Close()

	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	body := `{"steps":[
		{"method":"GET","url":"` + target.URL + `/a","extracts":[{"var":"uid","path":"uid"}]},
		{"method":"GET","url":"` + target.URL + `/b/{{uid}}"}
	]}`
	req := httptest.NewRequest("POST", "/api/v1/tests/debug-scenario", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleOperator))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Steps []struct {
			URL    string `json:"url"`
			Status int    `json:"status"`
			OK     bool   `json:"ok"`
		} `json:"steps"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if len(resp.Steps) != 2 {
		t.Fatalf("got %d steps, want 2: %+v", len(resp.Steps), resp.Steps)
	}
	// The dependent step's URL must be resolved and succeed.
	if resp.Steps[1].URL != target.URL+"/b/7" {
		t.Errorf("step 2 url = %q, want %s/b/7 (chain not resolved)", resp.Steps[1].URL, target.URL)
	}
	if resp.Steps[1].Status != 200 || !resp.Steps[1].OK {
		t.Errorf("step 2 = %d ok=%v, want 200 ok", resp.Steps[1].Status, resp.Steps[1].OK)
	}
}
