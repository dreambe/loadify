// Command workerd is a stateless load-generation worker. It dials the
// coordinator, registers, and runs assigned protocol load.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dreambe/loadify/internal/clusterauth"
	"github.com/dreambe/loadify/internal/config"
	"github.com/dreambe/loadify/internal/obs"
	"github.com/dreambe/loadify/internal/version"
	"github.com/dreambe/loadify/internal/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// -healthcheck probes the local /healthz endpoint and exits 0/1, so the
	// container HEALTHCHECK can run the binary itself — the distroless runtime
	// image has no shell/curl/wget to probe with.
	healthcheck := flag.Bool("healthcheck", false, "probe local /healthz and exit 0 (ok) / 1 (down)")
	flag.Parse()
	cfg := config.LoadWorker()
	if *healthcheck {
		os.Exit(probeHealth(cfg.HTTPAddr))
	}

	log := obs.NewLogger("workerd")
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

// probeHealth issues a GET to the local /healthz endpoint and returns a process
// exit code: 0 if it answers 200, 1 otherwise. addr is the listen address (e.g.
// ":8090"); a bare :port is probed on localhost.
func probeHealth(addr string) int {
	host := addr
	if strings.HasPrefix(addr, ":") {
		host = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + host + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return 0
	}
	return 1
}
