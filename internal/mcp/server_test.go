package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dreambe/loadify/internal/apiclient"
)

// fakeAPI stands in for apisrv: it records the test/run requests and returns
// canned ids and a completed run.
func fakeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/tests" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "test-1"})
		case r.URL.Path == "/api/v1/runs" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
		case strings.HasPrefix(r.URL.Path, "/api/v1/runs/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "run-1", "status": "completed", "summary": map[string]any{"total_requests": 1234}})
		case r.URL.Path == "/api/v1/workers":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"worker_id": "w1", "status": "healthy", "active_vus": 5}})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// roundtrip feeds one JSON-RPC request line through the server and decodes the
// single response.
func roundtrip(t *testing.T, srv *Server, req string) rpcResponse {
	t.Helper()
	var out strings.Builder
	if err := srv.Serve(context.Background(), strings.NewReader(req+"\n"), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resp rpcResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("decode response %q: %v", out.String(), err)
	}
	return resp
}

func TestInitializeAndToolsList(t *testing.T) {
	srv := NewServer(apiclient.New("http://unused", ""))

	resp := roundtrip(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}

	resp = roundtrip(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	b, _ := json.Marshal(resp.Result)
	for _, want := range []string{"loadify_quick_run", "loadify_run_status", "loadify_list_workers"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("tools/list missing %s: %s", want, b)
		}
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	srv := NewServer(apiclient.New("http://unused", ""))
	var out strings.Builder
	if err := srv.Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("notification should produce no response, got %q", out.String())
	}
}

func TestQuickRunToolCall(t *testing.T) {
	api := fakeAPI(t)
	defer api.Close()
	srv := NewServer(apiclient.New(api.URL, "tok"))

	req := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"loadify_quick_run","arguments":{"url":"http://x","vus":10,"duration_seconds":1}}}`
	resp := roundtrip(t, srv, req)
	if resp.Error != nil {
		t.Fatalf("tool call rpc error: %+v", resp.Error)
	}
	res, _ := resp.Result.(map[string]any)
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("tool reported error: %v", res["content"])
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "run-1") || !strings.Contains(text, "completed") {
		t.Errorf("unexpected quick_run result: %s", text)
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := NewServer(apiclient.New("http://unused", ""))
	resp := roundtrip(t, srv, `{"jsonrpc":"2.0","id":9,"method":"bogus/method"}`)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("expected method-not-found error, got %+v", resp.Error)
	}
}
