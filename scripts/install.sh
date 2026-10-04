#!/bin/bash
set -e

# Proxima VPN Node Agent - One-Click Installation Script
# Usage: bash <(curl -s PANEL_URL/scripts/install.sh) --server <panel-url> --token <reg-token>
# Registers first, then fetches the authoritative panel role: relay skips VPN
# software; exit/both install Xray. Existing VPN software is never removed.
# Options:
#   --server   Panel server URL (required)
#   --token    Registration token (required)
#   --name     Node display name (optional, defaults to hostname)
#   --country  Country code (optional, auto-detected if omitted)
#   --region   Region/city (optional, auto-detected if omitted)
#   --port     Service port (optional, defaults to 443)
#   --ports    Firewall ports/ranges to open, e.g. "443,20001-22000"
#              (optional, defaults to service port plus the speed-tier range)

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
PORTS=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --server)  SERVER="$2"; shift 2 ;;
        --token)   TOKEN="$2"; shift 2 ;;
        --name)    NAME="$2"; shift 2 ;;
        --country) COUNTRY="$2"; shift 2 ;;
        --region)  REGION="$2"; shift 2 ;;
        --port)    PORT="$2"; shift 2 ;;
        --ports)   PORTS="$2"; shift 2 ;;
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
            echo "  --ports    Firewall ports to open (default: service port + 20001-22000)"
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

# Speed-limited plans bind inbounds at 20000+Mbps (pkg/speedtier), so this range
# must stay open or those plans fail silently.
TIER_PORTS="20001-22000"
if [[ -z "$PORTS" ]]; then
    PORTS="${PORT},${TIER_PORTS}"
fi

# $PORTS reaches ufw/firewall-cmd, so validate before it hits a privileged command.
if [[ ! "$PORTS" =~ ^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$ ]]; then
    log_error "--ports must be comma-separated ports or ranges, e.g. 443,20001-22000"
    exit 1
fi
IFS=',' read -ra PORT_SPECS <<< "$PORTS"
for spec in "${PORT_SPECS[@]}"; do
    lo="${spec%%-*}"
    hi="${spec##*-}"
    if (( lo < 1 || lo > 65535 || hi < 1 || hi > 65535 || hi < lo )); then
        log_error "--ports entry '${spec}' is not a valid port or range"
        exit 1
    fi
done

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

# --- Install common dependencies ---
log_step "1/4" "Installing node-agent dependencies..."
# All roles apply kernel policy with nft and sysctl. iproute2 provides `ip`
# (and `tc` for exit speed limits); jq reads registration credentials and role.
# Do not install VPN-only tools until the panel has confirmed an exit role.
PACKAGE_MANAGER=""
if command -v apt-get &>/dev/null; then
    PACKAGE_MANAGER="apt-get"
    apt-get update -qq >/dev/null 2>&1
    apt-get install -y -qq curl ca-certificates jq systemd nftables iproute2 procps >/dev/null 2>&1
elif command -v yum &>/dev/null; then
    PACKAGE_MANAGER="yum"
    yum install -y -q curl ca-certificates jq systemd nftables iproute procps-ng >/dev/null 2>&1
elif command -v dnf &>/dev/null; then
    PACKAGE_MANAGER="dnf"
    dnf install -y -q curl ca-certificates jq systemd nftables iproute procps-ng >/dev/null 2>&1
else
    log_warn "Could not detect package manager. Ensure curl, CA certificates, jq, systemd, nftables, iproute2 (ip/tc), and procps (sysctl) are installed."
fi

# --- Download node-agent binary ---
log_step "2/4" "Installing node-agent..."
mkdir -p /usr/local/bin
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
log_step "3/4" "Registering with panel and resolving node role..."
REGISTER_CMD=(/usr/local/bin/node-agent register --server "$SERVER" --token "$TOKEN" --name "$NAME" --port "$PORT")

if [[ -n "$COUNTRY" ]]; then
    REGISTER_CMD+=(--country "$COUNTRY")
fi
if [[ -n "$REGION" ]]; then
    REGISTER_CMD+=(--region "$REGION")
fi

if ! "${REGISTER_CMD[@]}"; then
    log_error "Node registration failed"
    exit 1
fi

log_info "Node registered successfully"

# Registration persists credentials, not the role. Query the same authenticated
# control-plane endpoint the agent uses; never guess from a token or default an
# unreadable role to exit, which would install VPN software on a relay.
CONFIG_PATH="/etc/node-agent/config.json"
if ! NODE_ID=$(jq -er '.node_id | select(type == "string" and length > 0)' "$CONFIG_PATH") ||
   ! API_KEY=$(jq -er '.api_key | select(type == "string" and length > 0)' "$CONFIG_PATH"); then
    log_error "Could not read node registration credentials from $CONFIG_PATH"
    exit 1
fi
# Never follow redirects with this custom credential header: curl can forward it
# to another host. Disable curlrc overrides and explicitly require a 2xx response
# (curl -f alone accepts 3xx responses when redirect following is disabled).
if ! ROLE_RESPONSE=$(curl --disable -sS --no-location --connect-timeout 10 --max-time 30 \
    --write-out $'\n%{http_code}' -H "X-Node-Key: ${API_KEY}" "${SERVER}/api/v1/nodes/${NODE_ID}/role"); then
    log_error "Could not fetch the registered node role from the panel; no VPN software was installed"
    exit 1
fi
ROLE_HTTP_STATUS="${ROLE_RESPONSE##*$'\n'}"
if [[ ! "$ROLE_HTTP_STATUS" =~ ^2[0-9][0-9]$ ]]; then
    log_error "Node role request returned HTTP $ROLE_HTTP_STATUS; redirects and non-2xx responses are not accepted"
    exit 1
fi
ROLE_RESPONSE="${ROLE_RESPONSE%$'\n'*}"
if ! ROLE=$(printf '%s' "$ROLE_RESPONSE" | jq -er '.role | select(type == "string")'); then
    log_error "Panel returned an unreadable node role; no VPN software was installed"
    exit 1
fi
case "$ROLE" in
    relay|exit|both) ;;
    *)
        log_error "Panel returned unknown node role '$ROLE'; no VPN software was installed"
        exit 1
        ;;
esac
log_info "Node role: $ROLE"

# --- Install VPN-only dependencies and Xray-core for exit-capable roles ---
if [[ "$ROLE" != "relay" ]]; then
    log_step "4/4" "Installing Xray-core for $ROLE node..."
    case "$PACKAGE_MANAGER" in
        apt-get) apt-get install -y -qq unzip >/dev/null 2>&1 ;;
        yum)     yum install -y -q unzip >/dev/null 2>&1 ;;
        dnf)     dnf install -y -q unzip >/dev/null 2>&1 ;;
        *)       log_warn "Ensure unzip is installed for Xray-core installation." ;;
    esac
    # The panel reads per-user online IPs through GetStatsOnlineIpList, which older
    # cores do not serve - on one of those the concurrency figures are simply absent
    # and nothing looks wrong. Overridable so a pinned deployment can choose its own
    # version, but never silently below the floor.
    XRAY_MIN_VERSION="v25.1.1"
    XRAY_VERSION="${XRAY_VERSION:-}"
    if [[ -z "$XRAY_VERSION" ]]; then
        XRAY_VERSION=$(curl -fsSL "https://api.github.com/repos/XTLS/Xray-core/releases/latest" | jq -r .tag_name)
    fi
    if [[ -z "$XRAY_VERSION" || "$XRAY_VERSION" == "null" ]]; then
        log_error "Failed to fetch latest Xray-core version"
        exit 1
    fi

    # Compares dot-separated numbers, so 26.3.27 ranks above 26.3.9 where a string
    # comparison would not.
    version_below() {
        [[ "$(printf '%s\n%s\n' "${1#v}" "${2#v}" | sort -V | head -1)" == "${1#v}" && "${1#v}" != "${2#v}" ]]
    }
    if version_below "$XRAY_VERSION" "$XRAY_MIN_VERSION"; then
        log_error "Xray-core $XRAY_VERSION is older than the required $XRAY_MIN_VERSION"
        log_error "Per-user connection counting needs the GetStatsOnlineIpList stats RPC."
        exit 1
    fi
    log_info "Xray-core $XRAY_VERSION satisfies the $XRAY_MIN_VERSION minimum"

    XRAY_FILENAME="Xray-linux-64"
    if [[ "$ARCH" == "arm64" ]]; then
        XRAY_FILENAME="Xray-linux-arm64-v8a"
    fi

    XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_FILENAME}.zip"
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT

    if ! curl -fsSL -o "${TMPDIR}/xray.zip" "$XRAY_URL"; then
        log_error "Failed to download Xray-core from $XRAY_URL"
        exit 1
    fi

    unzip -q "${TMPDIR}/xray.zip" -d "${TMPDIR}/xray"
    cp "${TMPDIR}/xray/xray" /usr/local/bin/xray
    chmod +x /usr/local/bin/xray
    log_info "Xray-core ${XRAY_VERSION} installed"
else
    log_step "4/4" "Relay-only node: skipping VPN software installation"
    # Fresh-install provisioning only: leave any existing VPN software untouched.
fi

# --- Open firewall ports (best-effort) ---
# ufw wants a range as lo:hi, firewalld as lo-hi.
if command -v ufw &>/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
    for spec in "${PORT_SPECS[@]}"; do
        ufw allow "${spec/-/:}/tcp" >/dev/null 2>&1 || true
    done
    log_info "ufw: opened ${PORTS} (tcp)"
elif command -v firewall-cmd &>/dev/null && firewall-cmd --state &>/dev/null; then
    for spec in "${PORT_SPECS[@]}"; do
        firewall-cmd --permanent --add-port="${spec}/tcp" >/dev/null 2>&1 || true
    done
    firewall-cmd --reload >/dev/null 2>&1 || true
    log_info "firewalld: opened ${PORTS} (tcp)"
else
    log_warn "No active ufw or firewalld found; open ${PORTS}/tcp manually if a firewall is in use"
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
