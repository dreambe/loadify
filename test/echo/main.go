// Command echo is a tunable multi-protocol target server used by integration
// and e2e tests and by docker-compose. It speaks HTTP, WebSocket, SSE and gRPC
// (the standard health service) so every loadify driver has something to hit.
//
// HTTP (/) query params tune behaviour:
//
//	?delay_ms=10     fixed latency before responding
//	?status=503      force a response status
//	?error_rate=0.1  fraction of requests that return 500
//
// Endpoints:
//
//	/            HTTP echo (see params above)
//	/healthz     liveness
//	/ws          WebSocket echo (echoes every frame back)
//	/sse         Server-Sent-Events stream (?events=N&interval_ms=M)
//
// gRPC (grpc.health.v1.Health) listens on ECHO_GRPC_ADDR (default :8089).
package main

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	addr := envOr("ECHO_ADDR", ":8088")
	grpcAddr := envOr("ECHO_GRPC_ADDR", ":8089")

	mux := http.NewServeMux()
	mux.HandleFunc("/", handle)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/ws", handleWS)
	mux.HandleFunc("/sse", handleSSE)

	go serveGRPC(grpcAddr)

	fmt.Println("echo target listening on", addr, "(grpc", grpcAddr+")")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if ms, err := strconv.Atoi(q.Get("delay_ms")); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	if rate, err := strconv.ParseFloat(q.Get("error_rate"), 64); err == nil && rate > 0 {
		if rand.Float64() < rate {
			http.Error(w, "injected error", http.StatusInternalServerError)
			return
		}
	}
	status := http.StatusOK
	if s, err := strconv.Atoi(q.Get("status")); err == nil && s != 0 {
		status = s
	}
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	fmt.Fprintf(w, "echo %s %s len=%d", r.Method, r.URL.Path, len(body))
}

// handleWS echoes every received frame back to the client.
func handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if err := c.Write(ctx, typ, data); err != nil {
			return
		}
	}
}

// handleSSE streams events; ?events=N (default 3) and ?interval_ms=M (default 5).
func handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events := 3
	if n, err := strconv.Atoi(r.URL.Query().Get("events")); err == nil && n > 0 {
		events = n
	}
	interval := 5 * time.Millisecond
	if m, err := strconv.Atoi(r.URL.Query().Get("interval_ms")); err == nil && m > 0 {
		interval = time.Duration(m) * time.Millisecond
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	for i := 0; i < events; i++ {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		fmt.Fprintf(w, "id: %d\ndata: tick %d\n\n", i, i)
		flusher.Flush()
		if i < events-1 {
			time.Sleep(interval)
		}
	}
}

func serveGRPC(addr string) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "grpc listen:", err)
		return
	}
	srv := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		fmt.Fprintln(os.Stderr, "grpc serve:", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
