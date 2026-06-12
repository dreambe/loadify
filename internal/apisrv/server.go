// Package apisrv implements the public REST + WebSocket API plane: it owns the
// metadata store, talks to the coordinator over gRPC and queries the metrics
// store for charts.
package apisrv

import (
	"context"
	_ "embed"
	"log/slog"
	"net/http"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// openAPISpec is the machine-readable API contract, served at /openapi.yaml so
// agents and tooling can discover the REST API.
//
//go:embed openapi.yaml
var openAPISpec []byte

// Server wires the REST/WS API.
type Server struct {
	pg          metaStore
	ch          metricsStore
	coord       loadifyv1.CoordinatorServiceClient
	log         *slog.Logger
	mux         *chi.Mux
	authmw      auth.Middleware
	feishu      *auth.FeishuClient
	jwtSecret   string
	jwtTTL      time.Duration
	frontendURL string
	webhookURL  string
}

// Config configures the Server.
type Config struct {
	Postgres    metaStore
	ClickHouse  metricsStore
	Coordinator loadifyv1.CoordinatorServiceClient
	Logger      *slog.Logger
	JWTSecret   string
	JWTTTL      time.Duration
	Feishu      *auth.FeishuClient
	FrontendURL string
	// WebhookURL, when set, receives a JSON POST whenever a run finishes.
	WebhookURL string
}

// New builds the Server and its routes.
func New(c Config) *Server {
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	ttl := c.JWTTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	s := &Server{
		pg: c.Postgres, ch: c.ClickHouse, coord: c.Coordinator, log: log,
		authmw:      auth.Middleware{Secret: c.JWTSecret},
		feishu:      c.Feishu,
		jwtSecret:   c.JWTSecret,
		jwtTTL:      ttl,
		frontendURL: c.FrontendURL,
		webhookURL:  c.WebhookURL,
	}
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
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	})

	viewer := s.authmw.Require(auth.RoleViewer)
	operator := s.authmw.Require(auth.RoleOperator)
	admin := s.authmw.Require(auth.RoleAdmin)

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth endpoints.
		r.Get("/auth/config", s.handleAuthConfig)
		r.Post("/auth/login", s.handleLogin)
		r.Get("/auth/feishu/login", s.handleFeishuLogin)
		r.Get("/auth/feishu/callback", s.handleFeishuCallback)
		r.With(viewer).Get("/auth/me", s.handleMe)
		r.With(viewer).Post("/auth/password", s.handleChangePassword)

		// Reads require a viewer (or higher).
		r.With(viewer).Get("/tests", s.handleListTests)
		r.With(viewer).Get("/tests/{id}", s.handleGetTest)
		r.With(viewer).Get("/runs", s.handleListRuns)
		r.With(viewer).Get("/runs/{id}", s.handleGetRun)
		r.With(viewer).Get("/runs/{id}/series", s.handleRunSeries)
		r.With(viewer).Get("/runs/{id}/export.csv", s.handleRunExport) // token via ?token= works too
		r.With(viewer).Get("/runs/{id}/live", s.handleRunLive) // websocket (token via ?token=)
		r.With(viewer).Get("/workers", s.handleListWorkers)

		// Mutations require an operator (or higher).
		r.With(operator).Post("/tests", s.handleCreateTest)
		r.With(operator).Put("/tests/{id}", s.handleUpdateTest)
		r.With(operator).Delete("/tests/{id}", s.handleDeleteTest)
		r.With(operator).Post("/tests/debug", s.handleDebugRequest)
		r.With(operator).Post("/runs", s.handleStartRun)
		r.With(operator).Post("/runs/{id}/stop", s.handleStopRun)

		// Schedules: viewer reads, operator manages.
		r.With(viewer).Get("/schedules", s.handleListSchedules)
		r.With(operator).Post("/schedules", s.handleCreateSchedule)
		r.With(operator).Post("/schedules/{id}/enabled", s.handleSetScheduleEnabled)

		// User management is admin-only.
		r.With(admin).Get("/users", s.handleListUsers)
		r.With(admin).Post("/users", s.handleCreateUser)
		r.With(admin).Patch("/users/{id}", s.handleUpdateUser)
		r.With(admin).Delete("/users/{id}", s.handleDeleteUser)
	})
	s.mux = r
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
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
