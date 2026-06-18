# Multi-stage build shared by all Go services. Select the binary with
# --build-arg SERVICE=<apisrv|coordinatord|workerd|loadifyctl>.
# The build stage runs on the BUILD platform and Go cross-compiles to the target
# arch (TARGETARCH), so multi-arch builds (amd64+arm64) are fast — no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
WORKDIR /src

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
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath \
    -ldflags "-s -w" \
    -o /out/app ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
