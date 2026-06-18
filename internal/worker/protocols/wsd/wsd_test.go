package wsd_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
	_ "github.com/dreambe/loadify/internal/worker/protocols/wsd"
	"github.com/coder/websocket"
)

// echoWSServer accepts WebSocket connections and echoes every frame.
func echoWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	}))
}

func TestWSDriverEcho(t *testing.T) {
	srv := echoWSServer(t)
	defer srv.Close()

	p, err := plan.Parse([]byte(`{"protocol":"websocket","websocket":{"url":"` + srv.URL +
		`","send_messages":["hello","world"],"expect_echo":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	drv, err := protocols.New(loadifyv1.Protocol_PROTOCOL_WEBSOCKET, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Prepare(ctx); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer drv.Teardown(context.Background())

	vu := &protocols.VU{ID: 1}
	var firstConnectUs int64
	for i := 0; i < 5; i++ {
		res := drv.Exec(ctx, vu)
		if !res.OK {
			t.Fatalf("iter %d not ok: kind=%q", i, res.ErrorKind)
		}
		if res.RecvBytes == 0 {
			t.Errorf("iter %d: expected echoed bytes", i)
		}
		if i == 0 {
			firstConnectUs = res.ConnectUs
			if firstConnectUs <= 0 {
				t.Errorf("first iteration should report connect time")
			}
		} else if res.ConnectUs != 0 {
			t.Errorf("iter %d should reuse the connection (connect=%d)", i, res.ConnectUs)
		}
		vu.Iteration++
	}
}

func TestWSDriverDialFailure(t *testing.T) {
	p, err := plan.Parse([]byte(`{"protocol":"websocket","websocket":{"url":"ws://127.0.0.1:1/nope","expect_echo":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	drv, err := protocols.New(loadifyv1.Protocol_PROTOCOL_WEBSOCKET, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = drv.Prepare(ctx)
	defer drv.Teardown(context.Background())
	res := drv.Exec(ctx, &protocols.VU{ID: 1})
	if res.OK {
		t.Fatal("expected dial failure")
	}
	if res.ErrorKind == "" {
		t.Error("expected an error kind")
	}
}
