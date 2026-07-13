# Proxima VPN - Development Makefile
# Run `make help` to see available targets

.DEFAULT_GOAL := help

# Binary output directory
BIN_DIR := ./bin

# Binary names
API_SERVER := $(BIN_DIR)/api-server
NODE_AGENT := $(BIN_DIR)/node-agent

# Go build flags
GO_BUILD_FLAGS := CGO_ENABLED=0 go build -trimpath

.PHONY: build build-api build-agent test lint \
        dev-api dev-web docker-up docker-down clean help

## Build targets

build: build-api build-agent ## Build all binaries (api-server and node-agent)

build-api: ## Build api-server binary
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD_FLAGS) -o $(API_SERVER) ./api-server/cmd/server

build-agent: ## Build node-agent binary
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD_FLAGS) -o $(NODE_AGENT) ./node-agent/cmd/agent

## Test & Lint

test: ## Run all Go tests
	go test ./...

lint: ## Run linters (golangci-lint + frontend eslint)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found, skipping Go lint"; \
	fi
	cd web && npm run lint

## Development

dev-api: ## Run api-server with go run
	go run ./api-server/cmd/server

dev-web: ## Run frontend dev server
	cd web && npm run dev

## Docker

docker-up: ## Start docker-compose services
	docker-compose up -d

docker-down: ## Stop docker-compose services
	docker-compose down

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
