# Loadify — 分布式压测平台 / Distributed Load-Testing Platform

`loadify` is a distributed load-testing platform supporting HTTP/HTTPS, gRPC,
WebSocket and SSE, with a declarative test builder plus embedded JavaScript
(goja) scripting, real-time + historical dashboards, JWT/RBAC + Feishu OAuth,
and Docker Compose / Kubernetes (Helm) deployment.

It lives in its own top-level directory and is independent of the rest of the
repository (the MediaCrawler downloader).

## Components

| Binary          | Role                                                              |
|-----------------|-------------------------------------------------------------------|
| `apisrv`        | Public REST + WebSocket API, auth, metadata, metrics queries      |
| `coordinatord`  | Run scheduler: worker registry, VU sharding, metric aggregation   |
| `workerd`       | Stateless load generator (goroutine VU pool, protocol drivers)    |
| `loadifyctl`     | CLI to drive runs from a terminal / CI                            |

## Data stores

- **PostgreSQL** — metadata & RBAC (users, test definitions, runs).
- **ClickHouse** — time-series metrics (per-second rollups, sampled raw rows).

## Quick start (dev)

```bash
make proto          # regenerate gRPC stubs (requires buf + protoc plugins)
make build          # build all binaries
make test           # unit tests
docker compose -f deploy/compose/docker-compose.yml up --build
```

See `/root/.claude/plans` (design) and the `Makefile` for the full task list.

## Layout

```
platform/
├── api/proto/loadify/v1   # gRPC/proto contracts (source of truth)
├── cmd/                  # thin entrypoints (apisrv, coordinatord, workerd, loadifyctl)
├── internal/             # private packages (apisrv, coordinator, worker, plan, metrics, ...)
├── migrations/           # postgres + clickhouse schema migrations
├── deploy/               # docker, compose, helm
├── test/                 # echo target + integration/e2e harness
└── web/                  # Next.js frontend
```
