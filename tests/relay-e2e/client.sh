#!/usr/bin/env bash
set -euo pipefail

query_value() { tr '&' '\n' <<<"$1" | sed -n "s/^$2=//p" | sed -n '1p'; }
load_profile() {
  local label=$1 link authority query
  link=$(<"/state/link-${label}.txt")
  authority=${link#vless://}
  UUID=${authority%%@*}
  HOST=${authority#*@}; HOST=${HOST%%:*}
  PORT=${authority#*:}; PORT=${PORT%%\?*}
  query=${link#*\?}; query=${query%%#*}
  PUBLIC=$(query_value "$query" pbk)
  SHORT=$(query_value "$query" sid)
  SNI=$(query_value "$query" sni)
  [[ "$HOST" == entry.relay-e2e.test && "$SNI" == decoy.relay-e2e.test && -n "$UUID" && -n "$PUBLIC" && -n "$SHORT" ]] || exit 1
}
start_client() {
  local endpoint=$1 remote_port=$2 socks_port=$3
  jq -n --arg endpoint "$endpoint" --arg uuid "$UUID" --arg public "$PUBLIC" \
    --arg short "$SHORT" --arg sni "$SNI" --argjson port "$remote_port" --argjson socks "$socks_port" '{
      inbounds:[{listen:"127.0.0.1",port:$socks,protocol:"socks",settings:{udp:true}}],
      outbounds:[{protocol:"vless",settings:{vnext:[{address:$endpoint,port:$port,users:[{
        id:$uuid,flow:"xtls-rprx-vision",encryption:"none"}]}]},
        streamSettings:{network:"tcp",security:"reality",realitySettings:{
          serverName:$sni,fingerprint:"chrome",publicKey:$public,shortId:$short,spiderX:""}}}]
    }' >"/tmp/client-${socks_port}.json"
  xray run -config "/tmp/client-${socks_port}.json" >/dev/null 2>&1 &
  for ((attempt=1; attempt<=40; attempt++)); do
    if ss -tln | grep -q ":${socks_port} "; then return; fi
    sleep 0.2
  done
  echo 'local Xray SOCKS listener did not start' >&2
  exit 1
}
marker() {
  curl --noproxy '' --socks5-hostname "127.0.0.1:$1" --connect-timeout 2 --max-time 6 -fsS \
    http://198.51.100.10:9090/ 2>/dev/null
}
healthy() {
  local label=$1 socks=$2 expected port response
  load_profile "$label"
  expected="PROXIMA_EXIT_${label}"
  port=$PORT
  start_client "$HOST" "$port" "$socks"
  response=$(marker "$socks") || { echo "healthy control ${label} failed" >&2; exit 1; }
  [[ "$response" == "$expected" ]] || { echo "healthy control ${label} wrong marker" >&2; exit 1; }
}
denied() {
  local socks=$1
  if marker "$socks" >/dev/null; then echo 'unexpected tunnel success' >&2; exit 1; fi
}
case "$E2E_MODE" in
  positive)
    healthy A 1101
    healthy B 1102
    udp-probe probe 127.0.0.1:1101 PROXIMA_EXIT_A
    udp-probe probe 127.0.0.1:1102 PROXIMA_EXIT_B
    echo 'TCP and SOCKS UDP marker/nonce probes passed for both Exits'
    ;;
  negative)
    healthy A 1101; healthy B 1102
    [[ "$PORT" == 24444 ]] || exit 1
    local_port=1110
    load_profile A
    [[ "$PORT" == 24443 ]] || exit 1
    for ip in "$EXIT_A_BACK_IP" "$EXIT_B_BACK_IP"; do
      ping -c 1 -W 2 "$ip" >/dev/null
    done
    start_client "$HOST" 24446 "$local_port"; denied "$local_port"
    start_client "$HOST" 24444 1111; denied 1111
    start_client "$EXIT_A_BACK_IP" 8443 1112; denied 1112
    load_profile B
    start_client "$HOST" 24443 1113; denied 1113
    start_client "$EXIT_B_BACK_IP" 8443 1114; denied 1114
    load_profile B
    UUID_B=$UUID; PUBLIC_B=$PUBLIC; SHORT_B=$SHORT; SNI_B=$SNI
    load_profile A
    UUID_A=$UUID; PUBLIC_A=$PUBLIC; SHORT_A=$SHORT; SNI_A=$SNI
    UUID=$UUID_B; PUBLIC=$PUBLIC_B; SHORT=$SHORT_B; SNI=$SNI_B
    start_client "$HOST" 24443 1115; denied 1115
    UUID=$UUID_A; PUBLIC=$PUBLIC_A; SHORT=$SHORT_A; SNI=$SNI_A
    UUID=$UUID_B
    start_client "$HOST" 24443 1116; denied 1116
    load_profile B
    UUID=$UUID_A; PUBLIC=$PUBLIC_A; SHORT=$SHORT_A; SNI=$SNI_A
    start_client "$HOST" 24444 1117; denied 1117
    load_profile B
    UUID=$UUID_A
    start_client "$HOST" 24444 1118; denied 1118
    healthy A 1121; healthy B 1122
    echo 'all negative probes denied between healthy controls'
    ;;
  legacy-deny)
    healthy A 1101
    load_profile A
    start_client "$HOST" 24445 1103; denied 1103
    healthy B 1102
    ;;
  recovery)
    healthy A 1101; healthy B 1102
    load_profile A
    start_client "$HOST" 24445 1103
    [[ $(marker 1103) == PROXIMA_EXIT_A ]] || exit 1
    ;;
  lkg)
    healthy A 1101; healthy B 1102
    ;;
  *) exit 1 ;;
esac
