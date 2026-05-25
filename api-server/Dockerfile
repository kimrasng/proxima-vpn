# Build stage
FROM golang:1.24-alpine AS builder

ENV GOTOOLCHAIN=auto

WORKDIR /app

# Copy go.work and all module files
COPY go.work go.work
COPY api-server/go.mod api-server/go.mod
COPY node-agent/go.mod node-agent/go.mod
COPY pkg/go.mod pkg/go.mod

# Download dependencies
RUN cd api-server && go mod download

# Copy source code
COPY pkg/ pkg/
COPY api-server/ api-server/
COPY node-agent/ node-agent/

# Build the binaries
RUN cd api-server && CGO_ENABLED=0 GOOS=linux go build -o /app/bin/api-server ./cmd/main.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/bin/node-agent-linux-amd64 ./node-agent/cmd/main.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /app/bin/node-agent-linux-arm64 ./node-agent/cmd/main.go

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

COPY --from=builder /app/bin/api-server /usr/local/bin/api-server
COPY --from=builder /app/bin/node-agent-linux-amd64 /app/downloads/node-agent-linux-amd64
COPY --from=builder /app/bin/node-agent-linux-arm64 /app/downloads/node-agent-linux-arm64
COPY scripts/ /app/scripts/

EXPOSE 2053

ENTRYPOINT ["api-server"]
