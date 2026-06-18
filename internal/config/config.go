// Package config loads service configuration from environment variables with
// sane defaults. Each service reads only the sections it needs.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// insecureJWTSecret is the development-only default. A production deployment
// must override LOADIFY_JWT_SECRET; APIServer.Validate refuses to start with
// this value when LOADIFY_ENV names a production environment.
const insecureJWTSecret = "dev-insecure-secret-change-me"

// Postgres holds metadata-store connection settings.
type Postgres struct {
	DSN string
}

// ClickHouse holds metrics-store connection settings.
type ClickHouse struct {
	Addr     string
	Database string
	Username string
	Password string
}

// APIServer configures the public REST/WS plane.
type APIServer struct {
	Env               string // deployment environment: dev (default) | prod
	HTTPAddr          string
	CoordinatorGRPC   string
	JWTSecret         string
	JWTTTLHours       int
	FeishuAppID       string
	FeishuAppSecret   string
	FeishuRedirectURL string
	FrontendURL       string
	WebhookURL        string
	AdminEmail        string
	AdminPassword     string
	Postgres          Postgres
	ClickHouse        ClickHouse
}

// Coordinator configures the scheduler plane.
type Coordinator struct {
	GRPCAddr           string
	HTTPAddr           string // healthz/metrics
	MaxConcurrentRuns  int    // admission cap; extra runs queue
	WorkerCPUMaxPct    int    // per-node protection threshold: workers at/above this CPU% take no new runs (0 = off)
	ClickHouse         ClickHouse
	Postgres           Postgres
}

// Worker configures a load-generation agent.
type Worker struct {
	WorkerID        string
	CoordinatorGRPC string
	HTTPAddr        string // healthz/metrics
	Region          string
}

// LoadAPIServer builds APIServer config from the environment.
func LoadAPIServer() APIServer {
	return APIServer{
		Env:               env("LOADIFY_ENV", "dev"),
		HTTPAddr:          env("LOADIFY_API_HTTP_ADDR", ":8080"),
		CoordinatorGRPC:   env("LOADIFY_COORDINATOR_GRPC", "coordinatord:7070"),
		JWTSecret:         env("LOADIFY_JWT_SECRET", insecureJWTSecret),
		JWTTTLHours:       EnvInt("LOADIFY_JWT_TTL_HOURS", 24),
		FeishuAppID:       env("LOADIFY_FEISHU_APP_ID", ""),
		FeishuAppSecret:   env("LOADIFY_FEISHU_APP_SECRET", ""),
		FeishuRedirectURL: env("LOADIFY_FEISHU_REDIRECT_URL", ""),
		FrontendURL:       env("LOADIFY_FRONTEND_URL", "http://localhost:3000"),
		WebhookURL:        env("LOADIFY_WEBHOOK_URL", ""),
		AdminEmail:        env("LOADIFY_ADMIN_EMAIL", ""),
		AdminPassword:     env("LOADIFY_ADMIN_PASSWORD", ""),
		Postgres:          loadPostgres(),
		ClickHouse:        loadClickHouse(),
	}
}

// LoadCoordinator builds Coordinator config from the environment.
func LoadCoordinator() Coordinator {
	return Coordinator{
		GRPCAddr:          env("LOADIFY_COORDINATOR_GRPC_ADDR", ":7070"),
		HTTPAddr:          env("LOADIFY_COORDINATOR_HTTP_ADDR", ":7071"),
		MaxConcurrentRuns: EnvInt("LOADIFY_MAX_CONCURRENT_RUNS", 8),
		// Default-on per-node protection: a worker pegged at/above 85% CPU stops
		// accepting new runs (set to 0 to disable the gate entirely).
		WorkerCPUMaxPct:   EnvInt("LOADIFY_WORKER_CPU_MAX_PCT", 85),
		ClickHouse:        loadClickHouse(),
		Postgres:          loadPostgres(),
	}
}

// LoadWorker builds Worker config from the environment.
func LoadWorker() Worker {
	return Worker{
		WorkerID:        envNonEmpty("LOADIFY_WORKER_ID", hostnameOr("worker")),
		CoordinatorGRPC: env("LOADIFY_COORDINATOR_GRPC", "coordinatord:7070"),
		HTTPAddr:        env("LOADIFY_WORKER_HTTP_ADDR", ":8090"),
		Region:          env("LOADIFY_WORKER_REGION", "default"),
	}
}

func loadPostgres() Postgres {
	return Postgres{
		DSN: env("LOADIFY_POSTGRES_DSN", "postgres://loadify:loadify@postgres:5432/loadify?sslmode=disable"),
	}
}

func loadClickHouse() ClickHouse {
	return ClickHouse{
		Addr:     env("LOADIFY_CLICKHOUSE_ADDR", "clickhouse:9000"),
		Database: env("LOADIFY_CLICKHOUSE_DB", "loadify"),
		Username: env("LOADIFY_CLICKHOUSE_USER", "default"),
		Password: env("LOADIFY_CLICKHOUSE_PASSWORD", ""),
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// envNonEmpty is like env but also falls back to def when the variable is set
// but empty — so a compose "${VAR:-}" passthrough doesn't blank out the value.
func envNonEmpty(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvInt reads an int env var with a default. A set-but-unparsable value is
// logged (rather than silently swallowed) before falling back to the default.
func EnvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
		slog.Warn("ignoring invalid integer env var; using default",
			"key", key, "value", v, "default", def)
	}
	return def
}

// IsProd reports whether the deployment environment is a production one.
func (c APIServer) IsProd() bool {
	return c.Env == "prod" || c.Env == "production"
}

// Validate fails fast on unsafe production configuration — most importantly a
// JWT secret left at the insecure development default.
func (c APIServer) Validate() error {
	if c.IsProd() && c.JWTSecret == insecureJWTSecret {
		return fmt.Errorf("config: LOADIFY_JWT_SECRET must be set to a non-default value when LOADIFY_ENV=%s", c.Env)
	}
	return nil
}

func hostnameOr(def string) string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return def
}
