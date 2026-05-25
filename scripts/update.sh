#!/bin/bash
set -e

# Usage: bash update.sh [--agent-only | --xray-only | --all]

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

UPDATE_AGENT=false
UPDATE_XRAY=false

case "${1:---all}" in
    --agent-only) UPDATE_AGENT=true ;;
    --xray-only)  UPDATE_XRAY=true ;;
    --all)        UPDATE_AGENT=true; UPDATE_XRAY=true ;;
    *)
        log_error "Usage: $0 [--agent-only | --xray-only | --all]"
        exit 1
        ;;
esac

if [[ $EUID -ne 0 ]]; then
    log_error "This script must be run as root"
    exit 1
fi

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *)
        log_error "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

CONFIG_FILE="/etc/node-agent/config.json"
if [[ ! -f "$CONFIG_FILE" ]]; then
    log_error "Config not found: $CONFIG_FILE"
    log_error "Is node-agent installed? Run install.sh first."
    exit 1
fi

SERVER=$(grep -o '"server_url"[[:space:]]*:[[:space:]]*"[^"]*"' "$CONFIG_FILE" | sed 's/.*"server_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
if [[ -z "$SERVER" ]]; then
    log_error "Could not read server URL from $CONFIG_FILE"
    exit 1
fi

if [[ "$UPDATE_AGENT" == true ]]; then
    log_info "Updating node-agent..."

    OLD_VERSION=$(/usr/local/bin/node-agent version 2>/dev/null || echo "unknown")

    systemctl stop node-agent

    AGENT_URL="${SERVER}/downloads/node-agent-linux-${ARCH}"
    curl -fsSL -o /usr/local/bin/node-agent "$AGENT_URL"
    chmod +x /usr/local/bin/node-agent

    systemctl start node-agent

    NEW_VERSION=$(/usr/local/bin/node-agent version 2>/dev/null || echo "unknown")
    log_info "node-agent updated: $OLD_VERSION -> $NEW_VERSION"
fi

if [[ "$UPDATE_XRAY" == true ]]; then
    log_info "Updating Xray-core..."

    OLD_VERSION=$(/usr/local/bin/xray version 2>/dev/null | head -1 || echo "unknown")

    XRAY_VERSION=$(curl -fsSL "https://api.github.com/repos/XTLS/Xray-core/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    if [[ -z "$XRAY_VERSION" ]]; then
        log_error "Failed to fetch latest Xray-core version"
        exit 1
    fi

    XRAY_FILENAME="Xray-linux-64"
    if [[ "$ARCH" == "arm64" ]]; then
        XRAY_FILENAME="Xray-linux-arm64-v8a"
    fi

    XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_FILENAME}.zip"
    TMPDIR=$(mktemp -d)
    curl -fsSL -o "${TMPDIR}/xray.zip" "$XRAY_URL"
    unzip -q "${TMPDIR}/xray.zip" -d "${TMPDIR}/xray"
    cp "${TMPDIR}/xray/xray" /usr/local/bin/xray
    chmod +x /usr/local/bin/xray
    rm -rf "$TMPDIR"

    systemctl restart node-agent

    log_info "Xray-core updated to ${XRAY_VERSION}"
fi

log_info "Update complete. Service status: $(systemctl is-active node-agent)"
