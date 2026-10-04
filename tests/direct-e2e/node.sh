#!/usr/bin/env bash
set -euo pipefail

test -s /state/exit-agent.json
mkdir -p /www
printf '%s\n' 'PROXIMA_DIRECT_E2E_OK' > /www/index.html
busybox httpd -f -p 127.0.0.1:9090 -h /www &
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=www.cloudflare.com' \
  -keyout /tmp/decoy.key -out /tmp/decoy.crt >/dev/null 2>&1
openssl s_server -quiet -accept 127.0.0.1:9443 \
  -cert /tmp/decoy.crt -key /tmp/decoy.key >/tmp/decoy.log 2>&1 &

exec node-agent run --config /state/exit-agent.json
