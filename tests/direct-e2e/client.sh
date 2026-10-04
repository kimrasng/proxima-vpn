#!/usr/bin/env bash
set -euo pipefail

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
[[ "$endpoint" == "$EXIT_IP" && "$exit_port" == 8443 && -n "$client_uuid" && -n "$public_key" && -n "$short_id" && -n "$server_name" ]] || {
  echo 'FAIL: direct subscription link lacks expected endpoint or Reality credentials' >&2
  exit 1
}

start_client() {
  local uuid=$1 socks_port=$2
  jq -n --arg endpoint "$endpoint" --arg uuid "$uuid" \
    --arg public_key "$public_key" --arg short_id "$short_id" --arg server_name "$server_name" \
    --argjson exit_port "$exit_port" --argjson socks_port "$socks_port" '{
    inbounds: [{listen:"127.0.0.1",port:$socks_port,protocol:"socks",settings:{udp:false}}],
    outbounds: [{protocol:"vless",settings:{vnext:[{address:$endpoint,port:$exit_port,users:[{
      id:$uuid,flow:"xtls-rprx-vision",encryption:"none"
    }]}]},streamSettings:{network:"tcp",security:"reality",realitySettings:{
      serverName:$server_name,fingerprint:"chrome",publicKey:$public_key,shortId:$short_id,spiderX:""
    }}}]
  }' > "/tmp/client-${socks_port}.json"
  xray run -config "/tmp/client-${socks_port}.json" > "/tmp/client-${socks_port}.log" 2>&1 &
  for ((attempt = 1; attempt <= 30; attempt++)); do
    if ss -tln | grep -q ":${socks_port} "; then return; fi
    sleep 0.2
  done
  cat "/tmp/client-${socks_port}.log"
  exit 1
}

fetch_marker() {
  curl --noproxy '' --socks5-hostname "127.0.0.1:$1" \
    --connect-timeout 2 --max-time 8 -fsS http://127.0.0.1:9090/
}

start_client "$client_uuid" 1080
marker=$(fetch_marker 1080) || { cat /tmp/client-1080.log; exit 1; }
[[ "$marker" == 'PROXIMA_DIRECT_E2E_OK' ]] || {
  echo "FAIL: unexpected marker: $marker" >&2
  exit 1
}
echo "PASS: direct VLESS Reality delivered HTTP marker: $marker"

start_client '00000000-0000-0000-0000-000000000001' 1081
if fetch_marker 1081; then
  echo 'FAIL: unauthorized client reached the exit' >&2
  exit 1
fi
echo 'PASS: unauthorized client UUID cannot reach the marker'
