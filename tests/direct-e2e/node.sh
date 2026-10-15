#!/usr/bin/env bash
set -euo pipefail

test -s /state/exit-agent.json
mkdir -p /www
printf '%s\n' 'PROXIMA_DIRECT_E2E_OK' > /www/index.html
busybox httpd -f -p 127.0.0.1:9090 -h /www &
# A destination that accepts a connection and then says nothing, forever. The
# account-cap stage holds a tunnel open against it. httpd (above) closes idle
# connections after 60s, which would end the held tunnel for reasons unrelated
# to enforcement. Port 9092 is inside the relaye2e fixture range.
python3 - <<'PY' &
import socket, threading, time
srv = socket.socket(); srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", 9092)); srv.listen(64)
conns = []
while True:
    c, _ = srv.accept(); conns.append(c)   # keep the reference; never read, never close
PY
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=www.cloudflare.com' \
  -keyout /tmp/decoy.key -out /tmp/decoy.crt >/dev/null 2>&1
openssl s_server -quiet -accept 127.0.0.1:9443 \
  -cert /tmp/decoy.crt -key /tmp/decoy.key >/tmp/decoy.log 2>&1 &

# The agent keeps its stats outbox beside its config, so it needs a writable
# directory; the state volume is mounted read-only. Run from a private copy.
install -d -m 700 /var/lib/node-agent
install -m 600 /state/exit-agent.json /var/lib/node-agent/agent.json

exec node-agent run --config /var/lib/node-agent/agent.json
