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

// TestDebugScenarioParamsAndReqBody verifies query params are interpolated then
// URL-encoded (not encoded as literal templates), and that the resolved request
// body is echoed back so the user can see exactly what was sent.
func TestDebugScenarioParamsAndReqBody(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"uid":7}`))
	}))
	defer target.Close()

	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	body := `{"steps":[
		{"method":"GET","url":"` + target.URL + `","extracts":[{"var":"uid","path":"uid"}]},
		{"method":"POST","url":"` + target.URL + `/search",
		 "params":[{"key":"q","value":"a b"},{"key":"id","value":"{{uid}}"}],
		 "body":"hello {{uid}}"}
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
			URL     string `json:"url"`
			ReqBody string `json:"req_body"`
		} `json:"steps"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "" || len(resp.Steps) != 2 {
		t.Fatalf("error=%q steps=%d", resp.Error, len(resp.Steps))
	}
	got := resp.Steps[1].URL
	if !strings.Contains(got, "q=a%20b") {
		t.Errorf("url %q missing encoded space param q=a%%20b", got)
	}
	if !strings.Contains(got, "id=7") {
		t.Errorf("url %q missing interpolated param id=7", got)
	}
	if resp.Steps[1].ReqBody != "hello 7" {
		t.Errorf("req_body = %q, want %q (resolved body echo)", resp.Steps[1].ReqBody, "hello 7")
	}
}
