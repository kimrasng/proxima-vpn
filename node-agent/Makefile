VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build build-all build-linux-amd64 build-linux-arm64 clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/node-agent ./cmd/main.go

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/node-agent-linux-amd64 ./cmd/main.go

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/node-agent-linux-arm64 ./cmd/main.go

build-all: build-linux-amd64 build-linux-arm64

clean:
	rm -rf bin/
