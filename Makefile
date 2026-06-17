SHELL := /bin/bash
GOPATH_BIN := $(shell go env GOPATH)/bin
BINS := apisrv coordinatord workerd loadifyctl loadify-mcp

.PHONY: all build proto tidy test vet lint run-echo clean help web-install web-build web-dev helm-lint e2e e2e-up e2e-down

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

web-install: ## Install frontend dependencies
	cd web && npm ci

web-build: ## Build the Next.js frontend
	cd web && npm run build

web-dev: ## Run the frontend dev server
	cd web && npm run dev

helm-lint: ## Lint the Helm chart (requires helm)
	helm lint deploy/helm/loadify

e2e-up: ## Bring up the full stack for E2E (docker compose)
	cd deploy/compose && docker compose up -d --build

e2e-down: ## Tear down the E2E stack
	cd deploy/compose && docker compose down -v

e2e: ## Run the Playwright smoke suite against the running stack (see e2e-up)
	cd web && npm ci && npm run e2e:install && npm run e2e

clean: ## Remove build artifacts
	rm -rf bin web/.next web/node_modules
