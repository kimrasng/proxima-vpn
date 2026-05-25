#!/bin/bash
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }

if [[ $EUID -ne 0 ]]; then
    echo -e "${RED}[ERROR]${NC} This script must be run as root"
    exit 1
fi

echo ""
echo "=== Proxima VPN Node Agent Uninstaller ==="
echo ""

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
log_warn "Note: The node entry on the panel must be removed manually."
echo ""
