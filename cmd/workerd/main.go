// Command workerd is a stateless load-generation worker. It dials the
// coordinator, registers, and runs assigned protocol load.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dreambe/loadify/internal/clusterauth"
	"github.com/dreambe/loadify/internal/config"
	"github.com/dreambe/loadify/internal/obs"
	"github.com/dreambe/loadify/internal/version"
	"github.com/dreambe/loadify/internal/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	log := obs.NewLogger("workerd")
	cfg := config.LoadWorker()
	log.Info("starting", "version", version.String(), "worker_id", cfg.WorkerID, "coordinator", cfg.CoordinatorGRPC)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := grpc.NewClient(cfg.CoordinatorGRPC,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(64<<20)),
		clusterauth.DialOption(cfg.ClusterToken),
	)
	if err != nil {
		log.Error("dial coordinator failed", "err", err)
		return
	}
	defer conn.Close()

	health := obs.HealthServer(cfg.HTTPAddr)
	defer func() { _ = health.Shutdown(context.Background()) }()

	agent := worker.NewAgent(cfg.WorkerID, cfg.Region, log)
	if err := agent.Run(ctx, conn); err != nil && ctx.Err() == nil {
		log.Error("agent stopped", "err", err)
	}
	log.Info("shutting down")
}
