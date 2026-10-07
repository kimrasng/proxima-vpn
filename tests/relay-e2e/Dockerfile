FROM golang:1.25-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        busybox \
        ca-certificates \
        curl \
        iproute2 \
        iputils-ping \
        jq \
        nftables \
        openssl \
        postgresql-client \
        unzip \
    && rm -rf /var/lib/apt/lists/*

ARG TARGETARCH
ARG XRAY_VERSION=v26.7.28
RUN case "$TARGETARCH" in \
      amd64) asset=Xray-linux-64.zip ;; \
      arm64) asset=Xray-linux-arm64-v8a.zip ;; \
      *) exit 1 ;; \
    esac \
    && curl -fsSL --retry 3 -o /tmp/xray.zip \
      "https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${asset}" \
    && mkdir -p /usr/local/libexec \
    && unzip -p /tmp/xray.zip xray > /usr/local/libexec/xray \
    && chmod 755 /usr/local/libexec/xray \
    && rm /tmp/xray.zip

WORKDIR /src
COPY . .
RUN install -m 755 tests/relay-e2e/xray-wrapper.sh /usr/local/bin/xray
RUN go build -o /usr/local/bin/proxima-api ./api-server/cmd \
    && go build -tags relaye2e -o /usr/local/bin/node-agent ./node-agent/cmd \
    && GO111MODULE=off go build -o /usr/local/bin/udp-probe ./tests/relay-e2e/udp-probe
