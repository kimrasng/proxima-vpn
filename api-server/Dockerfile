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

# Build the binary
RUN cd api-server && CGO_ENABLED=0 GOOS=linux go build -o /app/bin/api-server ./cmd/main.go

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

COPY --from=builder /app/bin/api-server /usr/local/bin/api-server

EXPOSE 2053

ENTRYPOINT ["api-server"]
