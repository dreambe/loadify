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

## Next milestones (not yet built)

M2 finishing touches → M3 gRPC/WebSocket/SSE drivers → M4 goja scripting →
M5 frontend (Next.js) + dashboards → M6 JWT/RBAC + Feishu OAuth → M7 Helm/K8s.

## Try it (needs Docker daemon)

```bash
docker compose -f deploy/compose/docker-compose.yml up --build --scale workerd=2
# then drive a run:
go run ./cmd/loadifyctl --api http://localhost:8080 --url http://localhost:8088 --vus 50 --duration 15s
```
