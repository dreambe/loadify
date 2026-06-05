// Command apisrv is the public REST + WebSocket API plane.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/apisrv"
	"github.com/dreambe/loadify/internal/config"
	"github.com/dreambe/loadify/internal/obs"
	chstore "github.com/dreambe/loadify/internal/store/clickhouse"
	pgstore "github.com/dreambe/loadify/internal/store/postgres"
	"github.com/dreambe/loadify/internal/version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	log := obs.NewLogger("apisrv")
	log.Info("starting", "version", version.String())
	cfg := config.LoadAPIServer()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Postgres (with migrations).
	var pg *pgstore.Store
	if err := obs.Retry(ctx, "postgres", log, func(c context.Context) error {
		s, err := pgstore.Connect(c, cfg.Postgres)
		if err != nil {
			return err
		}
		if err := s.Migrate(c); err != nil {
			s.Close()
			return err
		}
		pg = s
		return nil
	}); err != nil {
		log.Error("postgres unavailable", "err", err)
		return
	}
	defer pg.Close()
	log.Info("postgres ready")

	// ClickHouse (read-only for apisrv; coordinator owns migrations/writes).
	var ch *chstore.Store
	if err := obs.Retry(ctx, "clickhouse", log, func(c context.Context) error {
		s, err := chstore.Connect(c, cfg.ClickHouse)
		if err != nil {
			return err
		}
		ch = s
		return nil
	}); err != nil {
		log.Error("clickhouse unavailable", "err", err)
		return
	}
	defer ch.Close()
	log.Info("clickhouse ready")

	conn, err := grpc.NewClient(cfg.CoordinatorGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial coordinator failed", "err", err)
		return
	}
	defer conn.Close()
	coord := loadifyv1.NewCoordinatorServiceClient(conn)

	srv := apisrv.New(apisrv.Config{Postgres: pg, ClickHouse: ch, Coordinator: coord, Logger: log})
	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	go func() {
		log.Info("http listening", "addr", cfg.HTTPAddr)
		if serr := httpSrv.ListenAndServe(); serr != nil && serr != http.ErrServerClosed {
			log.Error("http serve failed", "err", serr)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
}
