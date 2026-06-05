// Command coordinatord is the run scheduler: it accepts worker connections and
// the apisrv control API, shards runs, aggregates metrics and writes rollups.
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/config"
	"github.com/dreambe/loadify/internal/coordinator"
	"github.com/dreambe/loadify/internal/obs"
	"github.com/dreambe/loadify/internal/store"
	chstore "github.com/dreambe/loadify/internal/store/clickhouse"
	"github.com/dreambe/loadify/internal/version"
	"google.golang.org/grpc"
)

func main() {
	log := obs.NewLogger("coordinatord")
	log.Info("starting", "version", version.String())
	cfg := config.LoadCoordinator()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Connect to ClickHouse and apply migrations (retry until available).
	var writer store.RollupWriter
	var ch *chstore.Store
	if err := obs.Retry(ctx, "clickhouse", log, func(c context.Context) error {
		s, err := chstore.Connect(c, cfg.ClickHouse)
		if err != nil {
			return err
		}
		if err := s.Migrate(c); err != nil {
			_ = s.Close()
			return err
		}
		ch = s
		return nil
	}); err != nil {
		log.Error("clickhouse unavailable", "err", err)
		return
	}
	writer = ch
	defer ch.Close()
	log.Info("clickhouse ready")

	svc := coordinator.New(writer, log)
	gsrv := grpc.NewServer(grpc.MaxRecvMsgSize(64 << 20))
	loadifyv1.RegisterWorkerServiceServer(gsrv, svc)
	loadifyv1.RegisterCoordinatorServiceServer(gsrv, svc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Error("listen failed", "err", err, "addr", cfg.GRPCAddr)
		return
	}
	health := obs.HealthServer(cfg.HTTPAddr)
	defer func() { _ = health.Shutdown(context.Background()) }()

	go func() {
		log.Info("grpc listening", "addr", cfg.GRPCAddr)
		if serr := gsrv.Serve(lis); serr != nil {
			log.Error("grpc serve failed", "err", serr)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	gsrv.GracefulStop()
}
