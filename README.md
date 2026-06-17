# Loadify — Distributed Load-Testing Platform

**English** · [中文](README.zh.md)

`loadify` is a distributed load-testing platform supporting **HTTP/HTTPS, gRPC,
WebSocket and SSE**, with a declarative test builder, a no-code **multi-step
scenario** builder (weighted traffic mix + chained requests), structured
**response assertions** (status / body / JSON-path), embedded **JavaScript
(goja) scripting**, real-time + historical dashboards, light/dark themes,
**JWT/RBAC + Feishu OAuth**, and Docker Compose / Kubernetes (Helm) deployment.

## Components

| Binary          | Role                                                              |
|-----------------|-------------------------------------------------------------------|
| `apisrv`        | Public REST + WebSocket API, auth, metadata, metrics queries      |
| `coordinatord`  | Run scheduler: worker registry, VU sharding, metric aggregation   |
| `workerd`       | Stateless load generator (goroutine VU pool, protocol drivers)    |
| `loadifyctl`    | CLI to drive runs from a terminal / CI                            |
| `web/`          | Next.js dashboard (live charts, test builder, user management)    |

## Data stores

- **PostgreSQL** — metadata & RBAC (users, test definitions, runs).
- **ClickHouse** — time-series metrics (per-second rollups, sampled raw rows).

## Capabilities

- **Protocols** — HTTP/HTTPS (httptrace phase timings), gRPC (dynamic unary
  invocation from a descriptor set or the global registry), WebSocket
  (persistent per-VU sessions), SSE (event streaming).
- **Scenarios (no-code)** — chain multiple HTTP steps in **sequence**, extracting
  JSON fields from one response into `{{variables}}` consumed by later steps
  (e.g. login → use the returned token), or mix interfaces by **weight** to model
  realistic traffic ratios. Compiled to a script at launch, so it reuses the
  script engine.
- **Assertions** — per-request checks on status code, raw body, or an extracted
  JSON path with `eq/ne/gt/lt/gte/lte/contains/exists`; failures are counted as
  errors and the reason (which check, what value) shows in the live log.
- **Scripting** — write a goja JS `iteration()` function using an injected
  `http` API; runs as a load scenario with per-iteration metrics.
- **Distribution** — coordinator shards the ramp across workers, merges
  per-second HdrHistograms exactly, and streams live ticks; apisrv relays them
  to the browser over WebSocket. Historical series are queried from ClickHouse.
- **Auth** — local email/password (bcrypt) and Feishu OAuth login, HS256 JWTs,
  and `viewer < operator < admin` role-based access control.
- **SLA thresholds** — k6-style pass/fail criteria (p50/p90/p95/p99, error rate,
  QPS) evaluated at run finalize; any breach fails the run.
- **Scheduling** — capacity-aware admission: runs queue when the cluster is at
  its concurrent-run cap or workers are CPU-saturated, and drain as slots free.
  Recurring **scheduled runs** (multi-replica-safe claiming) and **CSV export**
  of per-second series.
- **Frontend (Next.js)** — switchable Chinese/English UI (default Chinese);
  structured HTTP request + stepped ramp (stages) builders; live charts with
  hover tooltips; a toggleable response log with an errors-only filter; and
  side-by-side run comparison with color-coded deltas.

## Quick start (Docker Compose)

```bash
docker compose -f deploy/compose/docker-compose.yml up --build --scale workerd=2
# UI:   http://localhost:3000   (admin@loadify.local / admin12345)
# API:  http://localhost:8080
```

Drive a run from the CLI:

```bash
go run ./cmd/loadifyctl \
  --api http://localhost:8080 \
  --email admin@loadify.local --password admin12345 \
  --url http://echo-target:8088/ --vus 50 --duration 15s
```

Run a scripted scenario:

```bash
cat > scenario.js <<'JS'
function iteration() {
  var r = http.get("http://echo-target:8088/");
  if (!r.ok) throw "bad status " + r.status;
  http.post("http://echo-target:8088/", "payload");
}
JS
go run ./cmd/loadifyctl --api http://localhost:8080 \
  --email admin@loadify.local --password admin12345 \
  --script scenario.js --vus 25 --duration 20s
```

## Use from agents / automation

loadify is built to be driven by autonomous agents as well as people. Three
equivalent entry points:

- **MCP server** (`loadify-mcp`) — a Model Context Protocol server (stdio) that
  exposes tools (`loadify_quick_run`, `loadify_run_status`, `loadify_list_workers`)
  so any MCP client can create and run tests and read results. Register it:

  ```json
  { "mcpServers": { "loadify": {
      "command": "loadify-mcp",
      "env": { "LOADIFY_API": "http://localhost:8080", "LOADIFY_TOKEN": "<jwt>" } } } }
  ```

  An agent then calls, e.g., `loadify_quick_run({ "url": "https://api/health",
  "target_rps": 500, "duration_seconds": 60 })` and gets the pass/fail summary.

- **REST API** — described by a machine-readable OpenAPI spec served at
  `GET /openapi.yaml` (and in `internal/apisrv/openapi.yaml`). Authenticate via
  `POST /api/v1/auth/login`, then `POST /api/v1/tests` + `POST /api/v1/runs` +
  `GET /api/v1/runs/{id}`.

- **CLI** (`loadifyctl`) — one command drives create → run → wait → summary,
  handy in CI.

## Development

```bash
make build        # build all Go binaries into ./bin
make test         # go test -race ./...
make vet          # go vet ./...
go test -bench . ./internal/metrics   # micro-benchmark the hot metrics path
make web-install  # install frontend deps
make web-build    # build the Next.js frontend
make proto        # regenerate gRPC stubs (needs buf + protoc plugins)
```

Generated gRPC stubs under `api/gen/` are gitignored; CI regenerates them with
`buf generate`.

## Kubernetes (Helm)

> Deploying workers across multiple servers / nodes? See the deployment guide:
> [`docs/deployment.zh.md`](docs/deployment.zh.md) (bare-Docker multi-host and
> Kubernetes multi-node, networking/security, the single-coordinator caveat).

```bash
helm lint deploy/helm/loadify
helm install loadify deploy/helm/loadify \
  --set secret.postgresDSN="postgres://user:pass@pg:5432/loadify?sslmode=disable" \
  --set database.clickhouse.addr="clickhouse:9000" \
  --set secret.jwtSecret="$(openssl rand -hex 32)" \
  --set ingress.enabled=true --set ingress.host=loadify.example.com \
  --set workerd.autoscaling.enabled=true
```

Postgres and ClickHouse are expected to be provided externally (managed service
or in-cluster operator).

## Configuration (env, `LOADIFY_` prefix)

| Var | Default | Used by |
|-----|---------|---------|
| `LOADIFY_API_HTTP_ADDR` | `:8080` | apisrv |
| `LOADIFY_COORDINATOR_GRPC` | `coordinatord:7070` | apisrv, workerd |
| `LOADIFY_POSTGRES_DSN` | `postgres://loadify:loadify@postgres:5432/loadify?sslmode=disable` | apisrv |
| `LOADIFY_CLICKHOUSE_ADDR` | `clickhouse:9000` | apisrv, coordinatord |
| `LOADIFY_JWT_SECRET` | `dev-insecure-secret-change-me` | apisrv |
| `LOADIFY_JWT_TTL_HOURS` | `24` | apisrv |
| `LOADIFY_FEISHU_APP_ID` / `_APP_SECRET` / `_REDIRECT_URL` | — | apisrv |
| `LOADIFY_FRONTEND_URL` | `http://localhost:3000` | apisrv (OAuth redirect) |
| `LOADIFY_ADMIN_EMAIL` / `_ADMIN_PASSWORD` | — | apisrv (bootstrap admin) |
| `LOADIFY_WORKER_REGION` | `default` | workerd |

## Layout

```
api/proto/loadify/v1   # gRPC/proto contracts (source of truth)
cmd/                   # thin entrypoints (apisrv, coordinatord, workerd, loadifyctl)
internal/              # private packages (apisrv, coordinator, worker, plan, auth, script, ...)
  worker/protocols/    # httpd, grpcd, wsd, ssed drivers
  script/              # goja scripting engine
  auth/                # JWT, RBAC, Feishu OAuth, password hashing
migrations/            # postgres + clickhouse schema migrations
deploy/                # docker, compose, helm
web/                   # Next.js frontend
test/                  # multi-protocol echo target + e2e harness
```

---

# Loadify — load testing your whole team will actually use

> One platform for HTTP, gRPC, WebSocket and SSE. Build a test by clicking, not
> by writing YAML. Watch it live, prove your SLA, and share the result with a
> link. Driveable by humans, CI, **and AI agents**.

![Architecture](docs/images/architecture.svg)

## Why teams pick loadify

- 🧩 **Every protocol, one tool.** HTTP/HTTPS (with connect/TLS/TTFB phase
  timings), dynamic **gRPC**, persistent **WebSocket**, and **SSE** — no
  juggling four different testers.
- 🪄 **No-code, then code when you need it.** A visual builder for requests,
  **multi-step scenarios** (weighted traffic + chained requests with
  `{{var}}` extraction), and **JSON-path assertions** — drop into embedded
  **JavaScript** only when you want full control.
- 📊 **Live and honest metrics.** Real-time QPS / latency / error charts with a
  synced crosshair and an errors-only response log; full historical replay; and
  **side-by-side run comparison** with color-coded deltas. Crucially, loadify
  *tells you when a result can't be trusted* — it flags **coordinated
  omission**, **dropped iterations**, and even when **the load generator itself
  was the bottleneck**, so a degraded run never quietly reads "green".
- 🚦 **SLA gates that fail your build.** Set k6-style thresholds (p95, error
  rate, QPS); a breach fails the run — wire it straight into CI.
- 🌐 **Truly distributed.** Stateless workers dial out to a single coordinator;
  add capacity by starting more workers on more servers. **Capacity-aware
  admission** queues runs when the fleet is busy and shows **"queued · ETA"**.
- 🤝 **Built to be automated.** A clean **REST API + OpenAPI spec**, an **MCP
  server**, a **CLI**, and a **permanent personal token** — hand it to your
  agent and let it create tests and launch runs for you.
- 🔗 **Share without logins.** One click turns any run into a public,
  interactive share link (print/save-as-PDF included).
- 🎛️ **Polished UX.** Switchable **中文 / English**, light/dark themes, a
  precision-instrument visual language, keyboard-friendly, and exportable
  PNG/CSV.
- 🔒 **Enterprise-ready auth.** JWT sessions, **viewer/operator/admin RBAC**,
  bcrypt local login, and **Feishu (Lark) OAuth**.

## Screenshots

<!-- Drop PNGs into docs/images/ with these names and they render here. -->

| Dashboard & SLA trend | Live run |
|---|---|
| ![Dashboard](docs/images/dashboard.png) | ![Live run](docs/images/run-live.png) |

| Run comparison | Scenario builder |
|---|---|
| ![Compare](docs/images/compare.png) | ![Builder](docs/images/builder.png) |

## Get started in 30 seconds

```bash
docker compose -f deploy/compose/docker-compose.yml up --build --scale workerd=2
# UI: http://localhost:3000   ·   login admin@loadify.local / admin12345
```

Running workers across multiple servers? See **[docs/deployment.zh.md](docs/deployment.zh.md)**.
