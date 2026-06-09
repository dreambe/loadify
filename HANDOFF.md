# loadify — handoff

This archive is the **loadify** distributed load-testing platform, ready to drop
into a fresh GitHub repo `dreambe/loadify` at the **repo root**.

Module path: `github.com/dreambe/loadify` · binaries: `apisrv`, `coordinatord`,
`workerd`, `loadifyctl` · env prefix: `LOADIFY_`.

## What's verified working (Go 1.24+)

```bash
# regenerate gRPC stubs (only needed if you change *.proto)
go install github.com/bufbuild/buf/cmd/buf@v1.47.2
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
PATH="$(go env GOPATH)/bin:$PATH" buf generate

go build ./...        # ok
go vet ./...          # ok
go test -race ./...   # ok — includes an in-process distributed e2e over real gRPC
```

> The generated stubs under `api/gen/` are included so it builds out of the box.
> They are gitignored (CI regenerates them via `buf generate`).

## Push it to your repo

```bash
git init -b main
git add -A
git -c user.email=you@example.com -c user.name="you" commit -m "feat: loadify load-testing platform (M0 + HTTP M1)"
git remote add origin git@github.com:dreambe/loadify.git
git push -u origin main
```

## What's implemented

- **M0**: repo scaffolding, gRPC contracts (buf), 3 services + CLI, embedded
  Postgres/ClickHouse migrations, Docker + Compose, CI.
- **M1 (HTTP)**: workerd VU pool + ramp + tuned HTTP/HTTPS driver with httptrace
  phase timings; per-second HdrHistogram sampler; coordinator registry/sharding/
  exact cross-worker histogram merge + rollups + live ticks; apisrv REST +
  WebSocket live stream; ClickHouse series/summary queries.
- **M2**: finishing touches — `loadifyctl` authenticates (token or login) and
  drives HTTP/script runs; Makefile web/helm targets; run stop endpoint.
- **M3**: gRPC (dynamic unary), WebSocket (persistent per-VU) and SSE drivers,
  registered with the worker; multi-protocol echo target; driver unit tests.
- **M4**: embedded goja JavaScript scripting — per-VU runtimes, an injected
  `http` API, and a `script` plan protocol selected automatically per run.
- **M5**: Next.js frontend — login (local + Feishu), runs list/detail with
  live WebSocket charts + historical series, test builder, workers, user admin.
- **M6**: HS256 JWT, viewer/operator/admin RBAC, bcrypt local login, Feishu
  OAuth, users table + admin bootstrap.
- **M7**: Helm chart (apisrv/coordinatord/workerd/web + Secret, optional
  HPA + Ingress); verified with `helm lint`/`template`.

## Beyond the roadmap (added on request)

- Multi-worker distributed e2e test (3 workers, concurrent load, merged metrics).
- Live response-log streaming (sampled per-request observations, errors first)
  with a toggleable, errors-filterable UI panel.
- Chart hover tooltips; structured HTTP-request and stepped-ramp (stages)
  builders replacing raw JSON.
- SLA thresholds (internal/sla): k6-style pass/fail evaluated at run finalize;
  any breach fails the run; results shown per-check in the run page.
- Side-by-side run comparison page with color-coded deltas.
- Switchable zh/en UI (default Chinese). Frontend build added to CI.

## Verified

`go build ./...`, `go vet ./...`, `go test -race ./...` all pass; `next build`
compiles the frontend; `helm lint`/`helm template` validate the chart.

## Try it (needs Docker daemon)

```bash
docker compose -f deploy/compose/docker-compose.yml up --build --scale workerd=2
# UI at http://localhost:3000 (admin@loadify.local / admin12345); then drive a run:
go run ./cmd/loadifyctl --api http://localhost:8080 \
  --email admin@loadify.local --password admin12345 \
  --url http://echo-target:8088/ --vus 50 --duration 15s
```
