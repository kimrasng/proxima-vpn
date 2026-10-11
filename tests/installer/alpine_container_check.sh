#!/bin/sh
# Runs inside alpine:3.20 to exercise scripts/install.sh against real apk,
# busybox and OpenRC. Only the panel-side network calls are faked (curl shim):
# package installs and the OpenRC service are real.
set -eu
apk add --no-cache -q bash curl openrc >/dev/null
# The container did not boot OpenRC; declare it and enter the default runlevel
# the way an Alpine VM's init would, so services start for real.
sed -i 's/^#rc_sys=""/rc_sys="docker"/' /etc/rc.conf
openrc default >/dev/null 2>&1

REAL_CURL=$(command -v curl)
mkdir -p /shim
cat > /shim/curl <<EOF
#!/bin/sh
for a; do url="\$a"; done
case "\$url" in
  */downloads/node-agent-linux-*)
    while [ \$# -gt 0 ]; do [ "\$1" = -o ] && out="\$2"; shift; done
    cp /fake-agent "\$out" ;;
  */role) printf '{"role":"relay"}\n200' ;;
  *) exec $REAL_CURL "\$@" ;;
esac
EOF
chmod +x /shim/curl
# A fake agent: `register` writes credentials, `run` stays in the foreground
# like the real one so supervise-daemon has something to supervise.
cat > /fake-agent <<'EOF'
#!/bin/sh
case "$1" in
  register) printf '{"node_id":"n1","api_key":"k1","server_url":"https://panel.test"}' > /etc/node-agent/config.json ;;
  run) echo "agent running"; exec sleep 3600 ;;
esac
EOF
chmod +x /fake-agent

PATH="/shim:$PATH" bash /src/scripts/install.sh --server https://panel.test --token t0
echo "== service file"; head -3 /etc/init.d/node-agent
echo "== runlevel"; rc-update show default | grep node-agent
echo "== status"; rc-service node-agent status
sleep 2; echo "== log"; cat /var/log/proxima/node-agent.log
echo "== respawn after kill"; pkill -f "sleep 3600"; sleep 7; pgrep -f "sleep 3600" >/dev/null && echo "respawned"
echo "== sort -V"; printf '26.3.27\n26.3.9\n' | sort -V | head -1
echo "== uninstall"; printf '#!/bin/sh\nexit 1\n' > /usr/local/bin/node-agent
bash /src/scripts/uninstall.sh >/dev/null 2>&1 || true
test ! -e /etc/init.d/node-agent && ! rc-update show default | grep -q node-agent && echo "service removed"
