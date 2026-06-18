package ssed_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
	_ "github.com/dreambe/loadify/internal/worker/protocols/ssed"
)

func sseServer(t *testing.T, events int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < events; i++ {
			fmt.Fprintf(w, "id: %d\ndata: tick %d\n\n", i, i)
			f.Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}))
}

func TestSSEDriverStream(t *testing.T) {
	srv := sseServer(t, 5)
	defer srv.Close()

	p, err := plan.Parse([]byte(`{"protocol":"sse","sse":{"url":"` + srv.URL + `","max_events":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	drv, err := protocols.New(loadifyv1.Protocol_PROTOCOL_SSE, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Prepare(ctx); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer drv.Teardown(context.Background())

	res := drv.Exec(ctx, &protocols.VU{ID: 1})
	if !res.OK {
		t.Fatalf("not ok: kind=%q status=%d", res.ErrorKind, res.Status)
	}
	if res.TTFBUs <= 0 {
		t.Error("expected a time-to-first-event measurement")
	}
	if res.RecvBytes == 0 {
		t.Error("expected received bytes")
	}
}

func TestSSEDriverBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p, _ := plan.Parse([]byte(`{"protocol":"sse","sse":{"url":"` + srv.URL + `","max_events":1}}`))
	drv, _ := protocols.New(loadifyv1.Protocol_PROTOCOL_SSE, p)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = drv.Prepare(ctx)
	defer drv.Teardown(context.Background())
	res := drv.Exec(ctx, &protocols.VU{ID: 1})
	if res.OK {
		t.Fatal("expected failure on 503")
	}
	if res.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", res.Status)
	}
}
