SHELL := /bin/bash
GOPATH_BIN := $(shell go env GOPATH)/bin
BINS := apisrv coordinatord workerd loadifyctl

.PHONY: all build proto tidy test vet lint run-echo clean help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

all: proto build ## Regenerate protos and build everything

proto: ## Regenerate gRPC stubs from .proto (needs buf + protoc-gen-go[-grpc])
	PATH="$(GOPATH_BIN):$$PATH" buf generate

build: ## Build all service binaries into ./bin
	@mkdir -p bin
	@for b in $(BINS); do echo "build $$b"; go build -o bin/$$b ./cmd/$$b; done

tidy: ## go mod tidy
	go mod tidy

test: ## Run unit tests with the race detector
	go test -race ./...

vet: ## go vet
	go vet ./...

lint: ## golangci-lint (if installed)
	@command -v golangci-lint >/dev/null && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

run-echo: ## Run the multi-protocol echo target server
	go run ./test/echo

clean: ## Remove build artifacts
	rm -rf bin
