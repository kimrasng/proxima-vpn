# Proxima VPN - Development Makefile
# Run `make help` to see available targets

.DEFAULT_GOAL := help

# Binary output directory
BIN_DIR := ./bin

# Binary names
API_SERVER := $(BIN_DIR)/api-server
NODE_AGENT := $(BIN_DIR)/node-agent

# Go modules in this workspace (see go.work). Targets iterate these because a
# bare `go test ./...` at the workspace root matches no packages.
MODULES := api-server node-agent pkg

# Version stamped into the node-agent binary, which compares it against the
# version the panel targets to decide whether to self-update. Overridable:
# `make build-agent VERSION=v1.2.3`.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO_BUILD_FLAGS := CGO_ENABLED=0 go build -trimpath
AGENT_LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build build-api build-agent test vet lint fmt \
        dev-api dev-web docker-up docker-down docker-logs clean help

## Build targets

build: build-api build-agent ## Build all binaries (api-server and node-agent)

build-api: ## Build api-server binary
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD_FLAGS) -o $(API_SERVER) ./api-server/cmd

build-agent: ## Build node-agent binary (VERSION=... to stamp a version)
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD_FLAGS) $(AGENT_LDFLAGS) -o $(NODE_AGENT) ./node-agent/cmd

## Test & Lint

test: ## Run Go tests in every workspace module
	@set -e; for m in $(MODULES); do \
		echo "==> go test ./... ($$m)"; \
		(cd $$m && go test ./...); \
	done

vet: ## Run go vet in every workspace module
	@set -e; for m in $(MODULES); do \
		echo "==> go vet ./... ($$m)"; \
		(cd $$m && go vet ./...); \
	done

lint: vet ## Run linters (golangci-lint per module + frontend eslint)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		set -e; for m in $(MODULES); do \
			echo "==> golangci-lint run ($$m)"; \
			(cd $$m && golangci-lint run ./...); \
		done; \
	else \
		echo "golangci-lint not found, skipping Go lint"; \
	fi
	cd web && npm run lint

fmt: ## Format Go sources in every workspace module
	@set -e; for m in $(MODULES); do \
		echo "==> gofmt -w ($$m)"; \
		(cd $$m && gofmt -l -w .); \
	done

## Development

dev-api: ## Run api-server with go run
	go run ./api-server/cmd

dev-web: ## Run frontend dev server
	cd web && npm run dev

## Docker

docker-up: ## Start docker compose services
	docker compose up -d --build

docker-down: ## Stop docker compose services
	docker compose down

docker-logs: ## Tail api and web logs
	docker compose logs -f api web

## Cleanup

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

## Help

help: ## Show available targets
	@echo "Proxima VPN - Available targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo ""
