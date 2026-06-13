// Package apisrv implements the public REST + WebSocket API plane: it owns the
// metadata store, talks to the coordinator over gRPC and queries the metrics
// store for charts.
package apisrv

import (
	"context"
	_ "embed"
	"log/slog"
	"net/http"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/obs"
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
	// revCache memoizes per-user revocation state (disabled / creds-changed) so
	// token validation doesn't hit the database on every request.
	revCache sync.Map
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
	// Honor account disable / credential changes for already-issued tokens.
	s.authmw.Validate = s.validateClaims
	s.routes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)
	r.Use(corsMiddleware)
	r.Use(metricsMiddleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	})

	viewer := s.authmw.Require(auth.RoleViewer)
	operator := s.authmw.Require(auth.RoleOperator)
	admin := s.authmw.Require(auth.RoleAdmin)

	r.Route("/api/v1", func(r chi.Router) {
		// Record mutating actions (who/when/what/outcome) for operators+admins.
		r.Use(s.auditMiddleware)

		// Public auth endpoints.
		r.Get("/auth/config", s.handleAuthConfig)
		r.Post("/auth/login", s.handleLogin)
		r.Get("/auth/feishu/login", s.handleFeishuLogin)
		r.Get("/auth/feishu/callback", s.handleFeishuCallback)
		r.With(viewer).Get("/auth/me", s.handleMe)
		r.With(viewer).Post("/auth/password", s.handleChangePassword)
		r.With(viewer).Get("/auth/webhooks", s.handleGetWebhooks)
		r.With(viewer).Put("/auth/webhooks", s.handleSetWebhooks)

		// Reads require a viewer (or higher).
		r.With(viewer).Get("/tests", s.handleListTests)
		r.With(viewer).Get("/tests/{id}", s.handleGetTest)
		r.With(viewer).Get("/tests/{id}/trend", s.handleTestTrend)
		r.With(operator).Post("/tests/{id}/baseline", s.handleSetBaseline)
		r.With(viewer).Get("/runs", s.handleListRuns)
		r.With(viewer).Get("/runs/{id}", s.handleGetRun)
		r.With(viewer).Get("/runs/{id}/series", s.handleRunSeries)
		r.With(viewer).Get("/runs/{id}/samples", s.handleRunSamples)
		r.With(viewer).Get("/runs/{id}/export.csv", s.handleRunExport) // token via ?token= works too
		r.With(viewer).Get("/runs/{id}/report.html", s.handleRunReport) // token via ?token= works too
		r.With(viewer).Get("/runs/{id}/live", s.handleRunLive) // websocket (token via ?token=)
		r.With(viewer).Get("/workers", s.handleListWorkers)

		// Mutations require an operator (or higher).
		r.With(operator).Post("/tests", s.handleCreateTest)
		r.With(operator).Put("/tests/{id}", s.handleUpdateTest)
		r.With(operator).Delete("/tests/{id}", s.handleDeleteTest)
		r.With(operator).Post("/tests/debug", s.handleDebugRequest)
		r.With(operator).Post("/tests/import", s.handleImport)

		// Environments: viewer reads, operator manages.
		r.With(viewer).Get("/environments", s.handleListEnvironments)
		r.With(operator).Post("/environments", s.handleCreateEnvironment)
		r.With(operator).Put("/environments/{id}", s.handleUpdateEnvironment)
		r.With(operator).Delete("/environments/{id}", s.handleDeleteEnvironment)
		r.With(operator).Post("/runs", s.handleStartRun)
		r.With(operator).Post("/runs/{id}/stop", s.handleStopRun)

		// Schedules: viewer reads, operator manages.
		r.With(viewer).Get("/schedules", s.handleListSchedules)
		r.With(operator).Post("/schedules", s.handleCreateSchedule)
		r.With(operator).Put("/schedules/{id}", s.handleUpdateSchedule)
		r.With(operator).Delete("/schedules/{id}", s.handleDeleteSchedule)
		r.With(operator).Post("/schedules/{id}/enabled", s.handleSetScheduleEnabled)

		// User management is admin-only.
		r.With(admin).Get("/audit", s.handleListAudit)
		r.With(admin).Get("/users", s.handleListUsers)
		r.With(admin).Post("/users", s.handleCreateUser)
		r.With(admin).Patch("/users/{id}", s.handleUpdateUser)
		r.With(admin).Delete("/users/{id}", s.handleDeleteUser)
	})
	s.mux = r
}

// securityHeaders adds defensive response headers. The API serves JSON/HTML
// reports only, so a tight CSP is safe and shrinks the XSS surface. (Moving
// the SPA's JWT from localStorage to an httpOnly cookie is a larger change —
// tracked separately; this is the cheap first line of defense.)
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// The HTML report is self-contained (inline styles/SVG, no scripts).
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// metricsMiddleware records request count and latency by chi route pattern, so
// /metrics carries real API observability instead of only Go runtime stats.
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		obs.HTTPRequests.WithLabelValues(route, r.Method, statusClass(rec.status)).Inc()
		obs.HTTPDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

// statusRecorder captures the response status for metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	default:
		return "2xx"
	}
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
