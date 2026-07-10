# Multi-stage build shared by all Go services. Select the binary with
# --build-arg SERVICE=<apisrv|coordinatord|workerd|loadifyctl>.
# The build stage runs on the BUILD platform and Go cross-compiles to the target
# arch (TARGETARCH), so multi-arch builds (amd64+arm64) are fast — no QEMU.
#
# Restricted networks (e.g. China): override the runtime base image and the Go
# module proxy via build-args (set in deploy/compose/.env). Defaults are the
# upstream registries, so normal builds are unchanged.
ARG RUNTIME_BASE=gcr.io/distroless/static-debian12:nonroot

FROM --platform=$BUILDPLATFORM golang:1.25 AS build
WORKDIR /src

ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}

COPY go.mod go.sum ./
RUN go mod download

# Install buf + protoc plugins so the image regenerates the gRPC stubs itself:
# api/gen is gitignored and absent from a clean checkout's build context.
# Versions match CI (.github/workflows/ci.yml).
RUN go install github.com/bufbuild/buf/cmd/buf@v1.47.2 \
 && go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2 \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

COPY . .
# Regenerate gRPC stubs before building (overwrites any stale/absent api/gen).
RUN buf generate
ARG SERVICE=apisrv
ARG TARGETOS TARGETARCH
# -tags timetzdata embeds the IANA tz database into the binary, so the TZ env
# var works even on a minimal runtime base (alpine/scratch) that ships no
# /usr/share/zoneinfo — lets reports/run-names render in the operator's zone.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath \
    -tags timetzdata \
    -ldflags "-s -w" \
    -o /out/app ./cmd/${SERVICE}

FROM ${RUNTIME_BASE}
# Carry the CA bundle from the (Debian-based) build image so HTTPS to targets
# works on ANY runtime base. This frees restricted-network deploys to point
# RUNTIME_BASE at a reachable minimal image (e.g. docker.io alpine) instead of
# the gcr distroless mirror, without losing TLS trust. distroless already ships
# these certs, so the copy is a harmless overwrite there.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
