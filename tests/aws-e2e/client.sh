#!/usr/bin/env bash
set -euo pipefail

parse_link() {
  local link=$1 override=${2:-} authority rest hostport query
  authority=${link#vless://}
  uuid=${authority%%@*}
  rest=${authority#*@}
  hostport=${rest%%\?*}
  host=${hostport%:*}
  port=${hostport##*:}
  query=${rest#*\?}
  query=${query%%#*}
  q() { printf '%s\n' "$query" | tr '&' '\n' | sed -n "s/^$1=//p" | sed -n '1p'; }
  pbk=$(q pbk); sid=$(q sid); sni=$(q sni)
  [[ -n "$override" ]] && host=$override
  [[ -n "$uuid" && -n "$host" && -n "$port" && -n "$pbk" && -n "$sid" && -n "$sni" ]] || {
    echo 'FAIL: link lacks host, port, uuid, pbk, sid or sni' >&2
    return 1
  }
}

cmd=${1:?command required}
shift
case "$cmd" in
  parse)
    parse_link "$@"
    echo "host=$host port=$port sni=$sni sid=$sid"
    ;;
  start)
    name=$1; socks=$2; link=$3
    parse_link "$link" "${4:-}"
    jq -n --arg host "$host" --arg uuid "$uuid" --arg pbk "$pbk" --arg sid "$sid" --arg sni "$sni" \
      --argjson port "$port" --argjson socks "$socks" '{
      inbounds: [{listen:"127.0.0.1",port:$socks,protocol:"socks",settings:{udp:false}}],
      outbounds: [{protocol:"vless",settings:{vnext:[{address:$host,port:$port,users:[{
        id:$uuid,flow:"xtls-rprx-vision",encryption:"none"
      }]}]},streamSettings:{network:"tcp",security:"reality",realitySettings:{
        serverName:$sni,fingerprint:"chrome",publicKey:$pbk,shortId:$sid,spiderX:""
      }}}]
    }' >"/tmp/xray-${name}.json"
    sudo systemctl stop "xray-${name}" 2>/dev/null || true
    sudo systemctl reset-failed "xray-${name}" 2>/dev/null || true
    sudo systemd-run --quiet --unit "xray-${name}" /usr/local/bin/xray run -config "/tmp/xray-${name}.json"
    for _ in $(seq 1 50); do
      ss -tln | grep -q ":${socks} " && exit 0
      sleep 0.2
    done
    sudo journalctl -u "xray-${name}" --no-pager | tail -20 >&2
    exit 1
    ;;
  fetch)
    curl --noproxy '' --socks5-hostname "127.0.0.1:$1" --connect-timeout 5 --max-time 15 -fsS "$2"
    ;;
  hold)
    socks=$1; target_ip=$2; target_port=$3; seconds=$4; role=$5
    IFS=. read -r a b c d <<<"$target_ip"
    # SOCKS5 greeting, then CONNECT to an IPv4 address (ATYP 0x01).
    exec {fd}<>"/dev/tcp/127.0.0.1/${socks}"
    printf '\x05\x01\x00' >&"${fd}"
    head -c 2 <&"${fd}" >/dev/null
    request="\\x05\\x01\\x00\\x01$(printf '\\x%02x' "$a" "$b" "$c" "$d")$(printf '\\x%02x' $((target_port >> 8)) $((target_port & 255)))"
    # shellcheck disable=SC2059
    printf "$request" >&"${fd}"
    reply=$(head -c 10 <&"${fd}" | od -An -tx1 | tr -d ' \n')
    [[ "$reply" == 0500* ]] || { echo "FAIL[${role}]: SOCKS CONNECT refused (reply=${reply})"; exit 1; }
    echo "ESTABLISHED role=${role}"
    start=$(date +%s)
    rc=0
    # read fails on EOF/reset (the exit closed the tunnel) or when the hold time runs out.
    read -r -t "${seconds}" -u "${fd}" _ || rc=$?
    echo "HOLD_RESULT role=${role} rc=${rc} elapsed=$(($(date +%s) - start))s"
    ;;
  *)
    echo "unknown command: $cmd" >&2
    exit 2
    ;;
esac
