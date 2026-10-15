#!/usr/bin/env bash
set -euo pipefail

# Account cap enforcement: one account (cap 1) connects from two different
# client IPs over the SAME shared UUID. The newer address must be disconnected
# by the panel's address-cap enforcement while the older one stays up.
#
# Roles (CAP_ROLE): "first" connects first and holds a long-lived download
# open; "second" connects after "first" is established and must be cut off.
# Both write their result to /result so the runner can assert on it.

link=$(cat /state/vless-link.txt)
authority=${link#vless://}
client_uuid=${authority%%@*}
endpoint_and_query=${authority#*@}
endpoint=${endpoint_and_query%%:*}
port_and_query=${endpoint_and_query#*:}
exit_port=${port_and_query%%\?*}
query=${link#*\?}
query=${query%%#*}

query_value() {
  printf '%s\n' "$query" | tr '&' '\n' | sed -n "s/^$1=//p" | sed -n '1p'
}
public_key=$(query_value pbk)
short_id=$(query_value sid)
server_name=$(query_value sni)

socks_port=1080
jq -n --arg endpoint "$endpoint" --arg uuid "$client_uuid" \
  --arg public_key "$public_key" --arg short_id "$short_id" --arg server_name "$server_name" \
  --argjson exit_port "$exit_port" --argjson socks_port "$socks_port" '{
  inbounds: [{listen:"127.0.0.1",port:$socks_port,protocol:"socks",settings:{udp:false}}],
  outbounds: [{protocol:"vless",settings:{vnext:[{address:$endpoint,port:$exit_port,users:[{
    id:$uuid,flow:"xtls-rprx-vision",encryption:"none"
  }]}]},streamSettings:{network:"tcp",security:"reality",realitySettings:{
    serverName:$server_name,fingerprint:"chrome",publicKey:$public_key,shortId:$short_id,spiderX:""
  }}}]
}' > /tmp/client.json
xray run -config /tmp/client.json > /tmp/client.log 2>&1 &
for ((attempt = 1; attempt <= 30; attempt++)); do
  ss -tln | grep -q ":${socks_port} " && break
  sleep 0.2
done

fetch() {
  curl --noproxy '' --socks5-hostname "127.0.0.1:${socks_port}" \
    --connect-timeout 3 --max-time "$2" -fsS "http://198.51.100.10:9090/$1"
}

role=${CAP_ROLE:?CAP_ROLE required}
hold_seconds=${CAP_HOLD_SECONDS:-150}

# Keep one tunnel open and idle for the whole test, with no transfer in flight.
# This must NOT be a slow download: the Exit shares one download-direction gate
# per UUID, so a slow transfer would stall every other client of the account
# and make the second client time out for a reason unrelated to the cap.
# Enforcement only needs the tunnel to be established, which an idle SOCKS
# stream to the silent listener on 9092 is.
hold() {
  local socks_fd
  exec {socks_fd}<>/dev/tcp/127.0.0.1/${socks_port}
  printf '\x05\x01\x00' >&${socks_fd}
  head -c 2 <&${socks_fd} >/dev/null
  # SOCKS5 CONNECT 198.51.100.10:9092 (ATYP IPv4 = 0xc6 0x33 0x64 0x0a, port 0x2384)
  printf '\x05\x01\x00\x01\xc6\x33\x64\x0a\x23\x84' >&${socks_fd}
  head -c 10 <&${socks_fd} >/dev/null
  # Stay open until the Exit closes the tunnel or the hold time runs out.
  # read returns non-zero on EOF/reset, which is the signal we are waiting for.
  local rc=0
  read -r -t "${hold_seconds}" -u "${socks_fd}" _ || rc=$?
  exec {socks_fd}>&-
  return "${rc}"
}

# Prove the tunnel works before holding it, so a later cut-off is attributable
# to enforcement and not to a broken setup.
# The first request after a cold start races Xray's own startup and the Exit's
# admission round trip, so retry briefly before declaring the tunnel broken.
marker=
for attempt in 1 2 3 4 5; do
  if marker=$(fetch "" 8 2>/tmp/fetch.err); then break; fi
  echo "retry[$role]: attempt ${attempt} failed: $(tr -d '\n' </tmp/fetch.err)"
  sleep 2
done
[[ -n "$marker" ]] || { echo "FAIL[$role]: tunnel never worked"; cat /tmp/client.log; exit 1; }
[[ "$marker" == 'PROXIMA_DIRECT_E2E_OK' ]] || { echo "FAIL[$role]: unexpected marker: $marker"; exit 1; }
echo "PASS[$role]: tunnel works before the cap is evaluated"

start=$(date +%s)
set +e
hold
status=$?
set -e
# read -t returns non-zero both on timeout (>128) and on EOF/reset (1); the
# caller decides by elapsed time, so the code is only recorded for diagnosis.
elapsed=$(( $(date +%s) - start ))
echo "HOLD_RESULT role=${role} curl_exit=${status} elapsed=${elapsed}s"
