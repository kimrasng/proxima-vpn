#!/usr/bin/env bash
# Sourced by run.sh after the panel and both exits are online. Needs from run.sh:
# api, pget, rsh, rcp, pub, priv, evidence, state, admin_token, panel_url.
wd="${state}/work"
mkdir -p "${wd}"
put() { printf '%s' "$3" >"${wd}/$1.$2"; }
get() { cat "${wd}/$1.$2"; }

node_field() { api GET /api/v1/admin/nodes "${admin_token}" | jq -er --arg n "$1" --arg f "$2" '.[] | select(.name == $n) | .[$f]'; }

setup_topology() {
  panel_pub=$(pub panel)
  rcp "${here}/marker.py" panel /tmp/marker.py
  rsh panel 'sudo systemd-run --quiet --unit marker python3 /tmp/marker.py'
  for vm in client1 client2; do
    rcp "${here}/client.sh" "${vm}" /tmp/client.sh
    rsh "${vm}" 'sudo bash -s' <<'EOF'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq && apt-get install -y -qq unzip curl jq >/dev/null
curl -fsSL --retry 3 -o /tmp/xray.zip https://github.com/XTLS/Xray-core/releases/download/v26.7.28/Xray-linux-64.zip
unzip -oq /tmp/xray.zip xray -d /usr/local/bin
chmod 755 /usr/local/bin/xray
EOF
  done
  register_node entry aws-entry relay 443

  exit1_id=$(node_field aws-exit-1 id); exit2_id=$(node_field aws-exit-2 id)
  entry_id=$(node_field aws-entry id)
  exit1_ip=$(node_field aws-exit-1 ip); exit2_ip=$(node_field aws-exit-2 ip)
  echo "exit ips registered by install.sh: exit1=${exit1_ip} exit2=${exit2_ip}"

  group_id=$(api POST /api/v1/admin/node-groups/ "${admin_token}" '{"name":"aws-e2e-access"}' | jq -er .id)
  api PUT "/api/v1/admin/node-groups/${group_id}/nodes" "${admin_token}" \
    "$(jq -nc --arg a "${exit1_id}" --arg b "${exit2_id}" '{node_ids:[$a,$b]}')" >/dev/null

  for id in "${exit1_id}" "${exit2_id}"; do
    api POST "/api/v1/admin/nodes/${id}/inbounds" "${admin_token}" \
      '{"protocol":"vless_reality","port":8443,"tag":"vless-in","settings":{"dest":"www.cloudflare.com:443","server_names":["www.cloudflare.com"]}}' >/dev/null
  done

  rsh panel "sudo docker exec -i pg psql -U postgres -d proxima -v ON_ERROR_STOP=1 -q -c \"INSERT INTO managed_entry_dns(owner_node_id,node_id,hostname,desired_ipv4,dns_status) VALUES ('${entry_id}','${entry_id}','entry.aws-e2e.test','$(pub entry)','ready')\"" </dev/null
  chain_id=$(api POST /api/v1/admin/node-chains/ "${admin_token}" \
    "$(jq -nc --arg e "${entry_id}" --arg x "${exit1_id}" '{name:"chain-exit1",entry_node_id:$e,entry_host:"entry.aws-e2e.test",entry_port:4443,exit_node_id:$x,exit_port:8443,transport:"tcp"}')" | jq -er .id)
  api PUT "/api/v1/admin/node-chains/${chain_id}/groups" "${admin_token}" \
    "$(jq -nc --arg g "${group_id}" '{group_ids:[$g]}')" >/dev/null
  for _ in $(seq 1 60); do
    api GET /api/v1/admin/node-chains "${admin_token}" | jq -e --arg c "${chain_id}" '.[] | select(.id == $c) | .apply.state == "applied"' >/dev/null && { echo 'PASS: entry->exit1 chain applied'; return; }
    sleep 3
  done
  api GET /api/v1/admin/node-chains "${admin_token}" >&2 || true
  echo 'FAIL: entry chain never reported applied' >&2
  return 1
}

mkuser() {
  local label=$1 maxdev=$2 maxcon=$3 n=$4 plan pw utoken i dev
  plan=$(api POST /api/v1/admin/plans/ "${admin_token}" \
    "$(jq -nc --arg g "${group_id}" --arg n "plan-${label}" --argjson d "${maxdev}" --argjson c "${maxcon}" \
      '{name:$n,node_group_id:$g,duration_days:30,max_devices:$d,max_concurrent:$c,prices:[],features:[]}')" | jq -er .id)
  pw="E2e-$(openssl rand -hex 10)!"
  api POST /api/v1/admin/users/ "${admin_token}" \
    "$(jq -nc --arg p "${plan}" --arg e "${label}@aws-e2e.test" --arg pw "${pw}" \
      '{email:$e,password:$pw,name:"aws e2e",plan_id:$p,is_active:true}')" >/dev/null
  utoken=$(api POST /api/v1/auth/login '' "$(jq -nc --arg e "${label}@aws-e2e.test" --arg pw "${pw}" '{email:$e,password:$pw}')" | jq -er .token)
  put "${label}" sub "$(api POST /api/v1/user/sub-token/regenerate "${utoken}" '{}' | jq -er .sub_token)"
  for i in $(seq 1 "${n}"); do
    dev=$(api POST /api/v1/user/devices "${utoken}" "{\"name\":\"${label}-${i}\"}")
    put "${label}" "device${i}.id" "$(jq -er .id <<<"${dev}")"
    put "${label}" "device${i}.uuid" "$(jq -er .xray_uuid <<<"${dev}")"
  done
}

link_for() {
  local label=$1 idx=$2 needle=$3 links line
  for _ in $(seq 1 60); do
    links=$(pget "/sub/$(get "${label}" sub)/$(get "${label}" "device${idx}.id")" | base64 -d 2>/dev/null | grep '^vless://' || true)
    line=$(grep -F -- "${needle}" <<<"${links}" | head -1 || true)
    [[ -n "${line}" ]] && { printf '%s' "${line}"; return 0; }
    sleep 3
  done
  echo "FAIL: no subscription link containing ${needle} for ${label}" >&2
  return 1
}

cstart() { rsh "$1" "bash /tmp/client.sh start $2 $3 '$4' $5"; }
cfetch() { rsh "$1" "bash /tmp/client.sh fetch $2 http://${panel_pub}:9090/"; }
log_has() { grep -q -- "$2" "$1" 2>/dev/null; }
wait_log() {
  local deadline=$((SECONDS + $3))
  while (( SECONDS < deadline )); do log_has "$1" "$2" && return 0; sleep 2; done
  return 1
}
elapsed_of() { sed -n 's/.*elapsed=\([0-9]*\)s.*/\1/p' "$1" | tail -1; }

test_connectivity() {
  mkuser conn 1 1 1
  local l body wrong
  l=$(link_for conn 1 "@${exit1_ip}:")
  cstart client1 direct 1080 "${l}" "$(pub exit1)"
  body=$(cfetch client1 1080)
  [[ "${body}" == *"src=$(pub exit1)"* ]] || { echo "FAIL: direct path did not egress from exit1: ${body}" >&2; return 1; }
  echo 'PASS: test 2a - direct client -> exit1 reached the marker, egress source is exit1'

  l=$(link_for conn 1 "@entry.aws-e2e.test:")
  cstart client1 relay 1081 "${l}" "$(pub entry)"
  body=$(cfetch client1 1081)
  [[ "${body}" == *"src=$(pub exit1)"* ]] || { echo "FAIL: relay path did not egress from exit1: ${body}" >&2; return 1; }
  echo 'PASS: test 2b - client -> entry -> exit1 reached the marker, egress source is exit1'

  wrong=${l/$(get conn device1.uuid)/00000000-0000-0000-0000-000000000001}
  cstart client1 wrong 1082 "${wrong}" "$(pub entry)"
  if cfetch client1 1082 >/dev/null 2>&1; then
    echo 'FAIL: an unknown UUID reached the marker through the relay' >&2
    return 1
  fi
  echo 'PASS: test 2c - unknown UUID is refused'
}

evict_scenario() {
  local label=$1 l1 l2
  mkuser "${label}" 2 1 2
  l1=$(link_for "${label}" 1 "@${exit1_ip}:"); l2=$(link_for "${label}" 2 "@${exit2_ip}:")
  u1=$(get "${label}" device1.uuid); u2=$(get "${label}" device2.uuid)
  cstart client1 "${label}a" 1090 "${l1}" "$(pub exit1)"
  cstart client1 "${label}b" 1091 "${l2}" "$(pub exit2)"
  cfetch client1 1090 >/dev/null && cfetch client1 1091 >/dev/null || { echo 'FAIL: tunnels did not work before the cap was evaluated' >&2; return 1; }
  first_log="${evidence}/${label}-first.log"; second_log="${evidence}/${label}-second.log"
  rsh client1 "bash /tmp/client.sh hold 1090 ${panel_pub} 9092 200 first" >"${first_log}" 2>&1 </dev/null &
  wait_log "${first_log}" ESTABLISHED 60 || { cat "${first_log}" >&2; echo 'FAIL: first tunnel never established' >&2; return 1; }
  rsh client1 "bash /tmp/client.sh hold 1091 ${panel_pub} 9092 200 second" >"${second_log}" 2>&1 </dev/null &
  wait_log "${second_log}" ESTABLISHED 60 || { cat "${second_log}" >&2; echo 'FAIL: second tunnel never established' >&2; return 1; }
  echo "PASS: ${label} - two UUIDs of one account (cap 1) are connected on different exits"
  wait_log "${second_log}" HOLD_RESULT 260 || { cat "${second_log}" >&2; echo 'FAIL: the newer UUID was never evicted' >&2; return 1; }
  (( $(elapsed_of "${second_log}") < 190 )) || { echo 'FAIL: newer UUID held its tunnel for the whole window' >&2; return 1; }
  log_has "${first_log}" HOLD_RESULT && { echo 'FAIL: the older UUID was also cut off' >&2; return 1; }
  echo "PASS: ${label} - newer UUID's tunnel was closed, older UUID kept its tunnel"
}

eviction_row() { api GET /api/v1/admin/uuid-evictions "${admin_token}" | jq -c --arg u "$1" '[.[] | select(.device_uuid == $u)] | .[0] // empty'; }

test_uuid_eviction() {
  evict_scenario evict
  local row
  for _ in $(seq 1 45); do
    row=$(eviction_row "${u2}")
    [[ -n "${row}" ]] && jq -e '(.pending_node_ids | length) == 0 and (.acknowledged_node_ids | length) == 2' <<<"${row}" >/dev/null && break
    sleep 2
  done
  printf '%s\n' "${row}" >"${evidence}/evict-row.json"
  jq -e '(.pending_node_ids | length) == 0 and (.acknowledged_node_ids | length) == 2' <<<"${row:-null}" >/dev/null \
    || { echo "FAIL: eviction was not acknowledged by both exits: ${row}" >&2; return 1; }
  echo 'PASS: test 3 - eviction epoch acknowledged by both exits'
  kill %1 %2 2>/dev/null || true
}

test_address_cap() {
  mkuser cap 1 1 1
  local l first_log2 second_log2
  l=$(link_for cap 1 "@${exit1_ip}:")
  cstart client1 cap 1100 "${l}" "$(pub exit1)"
  cstart client2 cap 1100 "${l}" "$(pub exit1)"
  first_log2="${evidence}/cap-first.log"; second_log2="${evidence}/cap-second.log"
  rsh client1 "bash /tmp/client.sh hold 1100 ${panel_pub} 9092 200 first" >"${first_log2}" 2>&1 </dev/null &
  first_pid=$!
  wait_log "${first_log2}" ESTABLISHED 60 || { cat "${first_log2}" >&2; echo 'FAIL: first client never established' >&2; return 1; }
  rsh client2 "bash /tmp/client.sh hold 1100 ${panel_pub} 9092 200 second" >"${second_log2}" 2>&1 </dev/null &
  second_pid=$!
  wait_log "${second_log2}" ESTABLISHED 60 || { cat "${second_log2}" >&2; echo 'FAIL: second client never established' >&2; return 1; }
  echo 'PASS: test 4 - one UUID connected from two public client addresses'
  wait_log "${second_log2}" HOLD_RESULT 260 || { cat "${second_log2}" >&2; echo 'FAIL: the newer client address was never cut off' >&2; return 1; }
  (( $(elapsed_of "${second_log2}") < 190 )) || { echo 'FAIL: newer address held its tunnel for the whole window' >&2; return 1; }
  kill -0 "${first_pid}" 2>/dev/null && ! log_has "${first_log2}" HOLD_RESULT \
    || { echo 'FAIL: the older address lost its tunnel' >&2; return 1; }
  cstart client1 capagain 1101 "${l}" "$(pub exit1)"
  cfetch client1 1101 >/dev/null || { echo 'FAIL: the shared UUID stopped working for new connections' >&2; return 1; }
  echo 'PASS: test 4 - newer address cut off, older kept, shared UUID still valid'
  kill "${first_pid}" "${second_pid}" 2>/dev/null || true
}

test_stuck_pending() {
  rsh exit2 "sudo iptables -I OUTPUT -p tcp --dport 2053 -m string --string 'revoked-devices/ack' --algo bm -j DROP" </dev/null
  rsh exit2 'sudo iptables -S OUTPUT | grep -q "revoked-devices/ack"' </dev/null \
    || { echo 'FAIL: could not install the ack-drop rule on exit2' >&2; return 1; }
  evict_scenario stuck
  local row
  for _ in $(seq 1 45); do
    row=$(eviction_row "${u2}")
    [[ -n "${row}" ]] && break
    sleep 2
  done
  sleep 60
  row=$(eviction_row "${u2}")
  printf '%s\n' "${row}" >"${evidence}/stuck-row-before-retry.json"
  jq -e --arg x "${exit2_id}" '.pending_node_ids == [$x]' <<<"${row:-null}" >/dev/null \
    || { echo "FAIL: expected exit2 to remain the only pending ack: ${row}" >&2; return 1; }
  api POST "/api/v1/admin/uuid-evictions/${u2}/retry" "${admin_token}" '{}' >/dev/null || true
  sleep 20
  row=$(eviction_row "${u2}")
  printf '%s\n' "${row}" >"${evidence}/stuck-row-after-retry.json"
  jq -e --arg x "${exit2_id}" '.pending_node_ids == [$x]' <<<"${row:-null}" >/dev/null \
    || { echo "FAIL: retry changed the pending set: ${row}" >&2; return 1; }
  cstart client1 stuckagain 1110 "$(link_for stuck 2 "@${exit2_ip}:")" "$(pub exit2)"
  if cfetch client1 1110 >/dev/null 2>&1; then
    echo 'FAIL: the evicted UUID connected again while its eviction is pending' >&2
    return 1
  fi
  echo 'PASS: test 5 - eviction stays pending on the silent exit, retry forges no ack, UUID stays banned'
  echo 'NOTE: this is the documented gap - no audited manual release exists for a pending eviction'
  kill %1 %2 2>/dev/null || true
}
