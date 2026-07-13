#!/bin/bash
set -e

# Proxima VPN Node Agent - One-Click Installation Script
# Usage: bash <(curl -s PANEL_URL/scripts/install.sh) --server <panel-url> --token <reg-token>
# Options:
#   --server   Panel server URL (required)
#   --token    Registration token (required)
#   --name     Node display name (optional, defaults to hostname)
#   --country  Country code (optional, auto-detected if omitted)
#   --region   Region/city (optional, auto-detected if omitted)
#   --port     Service port (optional, defaults to 443)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step()  { echo -e "${BLUE}[$1]${NC} $2"; }

# --- Parse arguments ---
SERVER=""
TOKEN=""
NAME=""
COUNTRY=""
REGION=""
PORT="443"

while [[ $# -gt 0 ]]; do
    case $1 in
        --server)  SERVER="$2"; shift 2 ;;
        --token)   TOKEN="$2"; shift 2 ;;
        --name)    NAME="$2"; shift 2 ;;
        --country) COUNTRY="$2"; shift 2 ;;
        --region)  REGION="$2"; shift 2 ;;
        --port)    PORT="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: bash install.sh --server <panel-url> --token <reg-token>"
            echo ""
            echo "Options:"
            echo "  --server   Panel server URL (required)"
            echo "  --token    Registration token (required)"
            echo "  --name     Node display name (optional)"
            echo "  --country  Country code (optional)"
            echo "  --region   Region/city (optional)"
            echo "  --port     Service port (default: 443)"
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

if [[ -z "$SERVER" ]]; then
    log_error "--server is required"
    echo "Usage: bash install.sh --server <panel-url> --token <reg-token>"
    exit 1
fi

if [[ -z "$TOKEN" ]]; then
    log_error "--token is required"
    echo "Usage: bash install.sh --server <panel-url> --token <reg-token>"
    exit 1
fi

# Strip trailing slash from server URL
SERVER="${SERVER%/}"

if [[ -z "$NAME" ]]; then
    NAME=$(hostname)
fi

# --- Check root privileges ---
if [[ $EUID -ne 0 ]]; then
    log_error "This script must be run as root"
    exit 1
fi

# --- Detect OS ---
if [[ "$(uname -s)" != "Linux" ]]; then
    log_error "Only Linux is supported"
    exit 1
fi

# --- Detect architecture ---
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)       ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        log_error "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

echo ""
echo "=== Proxima VPN Node Agent Installer ==="
echo "  Panel:  $SERVER"
echo "  Arch:   linux/$ARCH"
echo "  Name:   $NAME"
echo "======================================="
echo ""

# --- Install dependencies ---
log_step "1/4" "Installing dependencies..."
# iproute2 provides `tc`, used by the agent to enforce per-plan speed limits.
if command -v apt-get &>/dev/null; then
    apt-get update -qq >/dev/null 2>&1
    apt-get install -y -qq curl unzip jq iproute2 >/dev/null 2>&1
elif command -v yum &>/dev/null; then
    yum install -y -q curl unzip jq iproute >/dev/null 2>&1
elif command -v dnf &>/dev/null; then
    dnf install -y -q curl unzip jq iproute >/dev/null 2>&1
else
    log_warn "Could not detect package manager. Ensure curl, unzip, jq, and iproute2 (tc) are installed."
fi

# --- Download and install Xray-core ---
log_step "2/4" "Installing Xray-core..."
XRAY_VERSION=$(curl -fsSL "https://api.github.com/repos/XTLS/Xray-core/releases/latest" | jq -r .tag_name)
if [[ -z "$XRAY_VERSION" || "$XRAY_VERSION" == "null" ]]; then
    log_error "Failed to fetch latest Xray-core version"
    exit 1
fi

XRAY_FILENAME="Xray-linux-64"
if [[ "$ARCH" == "arm64" ]]; then
    XRAY_FILENAME="Xray-linux-arm64-v8a"
fi

XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_FILENAME}.zip"
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

if ! curl -fsSL -o "${TMPDIR}/xray.zip" "$XRAY_URL"; then
    log_error "Failed to download Xray-core from $XRAY_URL"
    exit 1
fi

unzip -q "${TMPDIR}/xray.zip" -d "${TMPDIR}/xray"
mkdir -p /usr/local/bin
cp "${TMPDIR}/xray/xray" /usr/local/bin/xray
chmod +x /usr/local/bin/xray
log_info "Xray-core ${XRAY_VERSION} installed"

# --- Download node-agent binary ---
log_step "3/4" "Installing node-agent..."
AGENT_URL="${SERVER}/downloads/node-agent-linux-${ARCH}"
AGENT_DOWNLOADED=false

if curl -fsSL -o /usr/local/bin/node-agent "$AGENT_URL" 2>/dev/null; then
    AGENT_DOWNLOADED=true
fi

if [[ "$AGENT_DOWNLOADED" != "true" ]]; then
    # Fallback: try GitHub releases
    GH_AGENT_URL="https://github.com/proximavpn/proxima-vpn/releases/latest/download/node-agent-linux-${ARCH}"
    if curl -fsSL -o /usr/local/bin/node-agent "$GH_AGENT_URL" 2>/dev/null; then
        AGENT_DOWNLOADED=true
    fi
fi

if [[ "$AGENT_DOWNLOADED" != "true" ]]; then
    log_error "Failed to download node-agent binary"
    log_error "Tried: $AGENT_URL"
    log_error "Tried: $GH_AGENT_URL"
    exit 1
fi

chmod +x /usr/local/bin/node-agent
log_info "node-agent installed"

# --- Create config directory ---
mkdir -p /etc/node-agent /var/log/proxima

# --- Register node with panel ---
log_step "4/4" "Registering with panel..."
REGISTER_CMD="/usr/local/bin/node-agent register --server ${SERVER} --token ${TOKEN} --name ${NAME} --port ${PORT}"

if [[ -n "$COUNTRY" ]]; then
    REGISTER_CMD="${REGISTER_CMD} --country ${COUNTRY}"
fi
if [[ -n "$REGION" ]]; then
    REGISTER_CMD="${REGISTER_CMD} --region ${REGION}"
fi

if ! $REGISTER_CMD; then
    log_error "Node registration failed"
    exit 1
fi

log_info "Node registered successfully"

# --- Open firewall ports (best-effort) ---
# The main service port plus the speed-tier port range (20001-22000), which the
# panel uses for per-plan speed-limited VLESS Reality inbounds.
TIER_PORT_RANGE_START=20001
TIER_PORT_RANGE_END=22000
if command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow "${PORT}/tcp" >/dev/null 2>&1 || true
    ufw allow "${TIER_PORT_RANGE_START}:${TIER_PORT_RANGE_END}/tcp" >/dev/null 2>&1 || true
    log_info "ufw: opened ${PORT} and ${TIER_PORT_RANGE_START}-${TIER_PORT_RANGE_END} (tcp)"
elif command -v firewall-cmd &>/dev/null && firewall-cmd --state &>/dev/null; then
    firewall-cmd --permanent --add-port="${PORT}/tcp" >/dev/null 2>&1 || true
    firewall-cmd --permanent --add-port="${TIER_PORT_RANGE_START}-${TIER_PORT_RANGE_END}/tcp" >/dev/null 2>&1 || true
    firewall-cmd --reload >/dev/null 2>&1 || true
    log_info "firewalld: opened ${PORT} and ${TIER_PORT_RANGE_START}-${TIER_PORT_RANGE_END} (tcp)"
fi

# --- Create systemd service ---
cat > /etc/systemd/system/node-agent.service <<EOF
[Unit]
Description=Proxima VPN Node Agent
After=network.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/node-agent run
Restart=always
RestartSec=5
LimitNOFILE=65535
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

# --- Enable and start service ---
systemctl daemon-reload
systemctl enable node-agent >/dev/null 2>&1
systemctl start node-agent

echo ""
echo "=== Installation Complete ==="
echo "  Status:  $(systemctl is-active node-agent)"
echo "  Config:  /etc/node-agent/config.json"
echo ""
echo "  Commands:"
echo "    systemctl status node-agent"
echo "    journalctl -u node-agent -f"
echo "    node-agent --help"
echo ""
echo "  Uninstall:"
echo "    bash <(curl -s ${SERVER}/scripts/uninstall.sh)"
echo "=============================="
