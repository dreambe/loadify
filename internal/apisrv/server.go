// Package apisrv implements the public REST + WebSocket API plane: it owns the
// metadata store, talks to the coordinator over gRPC and queries the metrics
// store for charts.
package apisrv

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/store/clickhouse"
	"github.com/dreambe/loadify/internal/store/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server wires the REST/WS API.
type Server struct {
	pg    *postgres.Store
	ch    *clickhouse.Store
	coord loadifyv1.CoordinatorServiceClient
	log   *slog.Logger
	mux   *chi.Mux
}

// Config configures the Server.
type Config struct {
	Postgres    *postgres.Store
	ClickHouse  *clickhouse.Store
	Coordinator loadifyv1.CoordinatorServiceClient
	Logger      *slog.Logger
}

// New builds the Server and its routes.
func New(c Config) *Server {
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Server{pg: c.Postgres, ch: c.ClickHouse, coord: c.Coordinator, log: log}
	s.routes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/tests", s.handleCreateTest)
		r.Get("/tests", s.handleListTests)
		r.Get("/tests/{id}", s.handleGetTest)

		r.Post("/runs", s.handleStartRun)
		r.Get("/runs", s.handleListRuns)
		r.Get("/runs/{id}", s.handleGetRun)
		r.Get("/runs/{id}/series", s.handleRunSeries)
		r.Get("/runs/{id}/live", s.handleRunLive) // websocket

		r.Get("/workers", s.handleListWorkers)
	})
	s.mux = r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withTimeout returns a context bounded for store/grpc calls.
func withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 15*time.Second)
}
