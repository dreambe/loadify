package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

// TestDatasetFeedsRequests: with a dataset, consecutive requests interpolate
// consecutive rows into URL path, query params, headers and body — cycling
// once the rows are exhausted.
func TestDatasetFeedsRequests(t *testing.T) {
	type seen struct {
		path, query, header, body string
	}
	var mu sync.Mutex
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		mu.Lock()
		got = append(got, seen{r.URL.Path, r.URL.RawQuery, r.Header.Get("X-User"), string(b[:n])})
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := &Driver{
		cfg: &plan.HTTPConfig{
			Method:  "POST",
			URL:     srv.URL + "/users/{{id}}",
			Params:  []plan.ScenarioParam{{Key: "name", Value: "{{user}}"}},
			Headers: map[string]string{"X-User": "{{user}}"},
			Body:    `{"user":"{{user}}"}`,
		},
		feed: []map[string]any{
			{"id": float64(1), "user": "alice"},
			{"id": float64(2), "user": "bob"},
		},
	}
	if err := d.Prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !d.dynamic {
		t.Fatal("driver should be dynamic with a feed")
	}
	for i := 0; i < 3; i++ {
		if res := d.Exec(context.Background(), &protocols.VU{ID: 1}); res.Status != 200 {
			t.Fatalf("req %d status = %d, err=%s", i, res.Status, res.ErrorKind)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	want := []seen{
		{"/users/1", "name=alice", "alice", `{"user":"alice"}`},
		{"/users/2", "name=bob", "bob", `{"user":"bob"}`},
		{"/users/1", "name=alice", "alice", `{"user":"alice"}`}, // cycled
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("req %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestGeneratorWithoutDataset: {{uuid}} in the body marks the request dynamic
// even without a feed, and each request gets a fresh value.
func TestGeneratorWithoutDataset(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		mu.Lock()
		bodies = append(bodies, string(b[:n]))
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := &Driver{cfg: &plan.HTTPConfig{Method: "POST", URL: srv.URL, Body: "{{uuid}}"}}
	if err := d.Prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !d.dynamic {
		t.Fatal("driver should be dynamic with a {{uuid}} template")
	}
	for i := 0; i < 2; i++ {
		if res := d.Exec(context.Background(), &protocols.VU{ID: 1}); res.Status != 200 {
			t.Fatalf("req %d status = %d", i, res.Status)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	uuidRe := regexp.MustCompile(`^[0-9a-f-]{36}$`)
	if len(bodies) != 2 || !uuidRe.MatchString(bodies[0]) || !uuidRe.MatchString(bodies[1]) {
		t.Fatalf("bodies = %q, want two uuids", bodies)
	}
	if bodies[0] == bodies[1] {
		t.Fatal("uuid should differ per request")
	}
}

// TestStaticStaysStatic: without templates or feed the prebuilt fast path is
// used and the request is byte-identical across iterations.
func TestStaticStaysStatic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := &Driver{cfg: &plan.HTTPConfig{Method: "GET", URL: srv.URL + "/static?a=1"}}
	if err := d.Prepare(context.Background()); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if d.dynamic {
		t.Fatal("driver should be static")
	}
	if res := d.Exec(context.Background(), &protocols.VU{ID: 1}); res.Status != 200 {
		t.Fatalf("status = %d", res.Status)
	}
}
