#!/usr/bin/env bash
set -euo pipefail

config="/state/${E2E_ROLE}-agent.json"
[[ -s "$config" ]] || { echo "missing agent config: $config" >&2; exit 1; }

if [[ "${E2E_ROLE}" == exit-* ]]; then
  mkdir -p /www
  printf '%s\n' "${E2E_MARKER}" > /www/index.html
  busybox httpd -f -p 127.0.0.1:9090 -h /www &
  udp-probe server 127.0.0.1:9091 "${E2E_MARKER}" &
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj '/CN=decoy.relay-e2e.test' \
    -keyout /tmp/decoy.key -out /tmp/decoy.crt >/dev/null 2>&1
  openssl s_server -quiet -accept 127.0.0.1:9443 \
    -cert /tmp/decoy.crt -key /tmp/decoy.key >/tmp/decoy.log 2>&1 &
fi

echo "starting real node-agent role=${E2E_ROLE} config=${config}"
exec node-agent run --config "$config"
