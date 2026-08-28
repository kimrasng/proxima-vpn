#!/bin/bash
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

if [[ $EUID -ne 0 ]]; then
    echo -e "${RED}[ERROR]${NC} This script must be run as root"
    exit 1
fi

echo ""
echo "=== Proxima VPN Node Agent Uninstaller ==="
echo ""

# Unregister from the panel first, while the binary and its saved API key
# (/etc/node-agent/config.json) still exist - see NodeAgentHandler.Unregister
# (api-server/internal/handlers/node_agent.go) and `node-agent unregister`
# (node-agent/cmd/main.go). Best-effort: if the panel is unreachable or the
# node was already removed there, we still proceed with the local teardown
# below rather than aborting, but warn so the admin knows to check the panel.
PANEL_UNREGISTERED=false
if [[ -x /usr/local/bin/node-agent && -f /etc/node-agent/config.json ]]; then
    log_info "Removing node from panel..."
    if /usr/local/bin/node-agent unregister; then
        PANEL_UNREGISTERED=true
    else
        log_error "Could not remove node from panel (server unreachable, or already removed)."
    fi
else
    log_warn "node-agent binary or config not found; skipping panel removal."
fi

log_info "Stopping node-agent service..."
systemctl stop node-agent 2>/dev/null || true
systemctl disable node-agent 2>/dev/null || true
rm -f /etc/systemd/system/node-agent.service
systemctl daemon-reload

log_info "Removing binaries..."
rm -f /usr/local/bin/node-agent
rm -f /usr/local/bin/xray

log_info "Removing configuration and logs..."
rm -rf /etc/node-agent
rm -rf /var/log/proxima

echo ""
log_info "Uninstallation complete."
if [[ "$PANEL_UNREGISTERED" == "true" ]]; then
    log_info "Node entry removed from panel."
else
    log_warn "Node entry was NOT removed from panel - remove it manually if it still appears there."
fi
echo ""
