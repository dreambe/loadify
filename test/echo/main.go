// Command echo is a tunable multi-protocol target server used by integration
// and e2e tests. M1 implements HTTP; gRPC/WebSocket/SSE are added with their
// drivers. Query params tune behaviour:
//
//	?delay_ms=10     fixed latency before responding
//	?status=503      force a response status
//	?error_rate=0.1  fraction of requests that return 500
package main

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	addr := ":8088"
	if v := os.Getenv("ECHO_ADDR"); v != "" {
		addr = v
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", handle)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	fmt.Println("echo target listening on", addr)
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
