#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
if (( $# == 0 )); then
  output="${root}/tests/evidence/entry-dns-sni/$(date -u +%Y%m%dT%H%M%SZ)-$$"
elif (( $# == 2 )) && [[ "$1" == --output ]]; then
  output=$2
else
  echo 'usage: run.sh [--output NEW_DIR]' >&2
  exit 2
fi
[[ ! -e "$output" && ! -L "$output" ]] || { echo 'output path already exists' >&2; exit 2; }
project="relay-e2e-$(openssl rand -hex 5)"
octet=$(( 100 + 0x$(openssl rand -hex 1) % 100 ))
third=$(( 20 + 0x$(openssl rand -hex 1) % 200 ))
export COMPOSE_PROJECT_NAME=$project E2E_IMAGE="relay-e2e:${project}"
export FRONT_SUBNET="10.${octet}.${third}.0/24" BACK_SUBNET="10.${octet}.$((third+1)).0/24"
export CLIENT_FRONT_IP="10.${octet}.${third}.10" RELAY_FRONT_IP="10.${octet}.${third}.20"
export CLIENT_BACK_IP="10.${octet}.$((third+1)).10" RELAY_BACK_IP="10.${octet}.$((third+1)).20"
export EXIT_A_BACK_IP="10.${octet}.$((third+1)).30" EXIT_B_BACK_IP="10.${octet}.$((third+1)).40"
compose=(docker compose -f "${root}/tests/relay-e2e/compose.yml")
work=$(mktemp -d)
# Masks credentials before anything reaches the (public) job log: UUIDs, JWTs,
# hex API keys/tokens, base64url Reality keys, short IDs and share links.
redact() {
  sed -E \
    -e 's/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/<uuid>/g' \
    -e 's/eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*/<jwt>/g' \
    -e 's#(vless|vmess|trojan|ss|hysteria2)://[^[:space:]"]*#\1://<link>#g' \
    -e 's/[0-9a-fA-F]{16,}/<hex>/g' \
    -e 's/[A-Za-z0-9_-]{40,}/<key>/g' \
    -e 's/((pbk|sid|password|pass|secret|token|api_key|privateKey|publicKey|shortIds?)"?[:=][[:space:]]*\[?"?)[^",&[:space:]]+/\1<redacted>/g'
}
# Failure-only view of the disposable topology: what the control plane thinks
# of each node and chain, the live nft policy, and redacted service logs.
diagnose() {
  echo '=== relay E2E failure diagnostics (credentials redacted) ==='
  "${compose[@]}" ps -a 2>&1 | redact || true
  echo '--- control-plane node/chain state ---'
  "${compose[@]}" exec -T postgres psql -U relay_e2e -d relay_e2e -P pager=off -c \
    "SELECT name, role, status, xray_running, config_hash <> '' AS has_config_hash,
            EXTRACT(EPOCH FROM NOW() - last_seen)::int AS last_seen_age_s,
            reality_sni_status, publish_direct, shaping_ok, shaping_mode
       FROM nodes ORDER BY name" \
    -c "SELECT name, entry_port, exit_port, mode, enabled, health FROM node_chains ORDER BY name" \
    2>&1 | redact || true
  local service
  for service in relay exit-a exit-b; do
    echo "--- nft ruleset: ${service} ---"
    "${compose[@]}" exec -T "$service" nft list ruleset 2>&1 | redact || true
  done
  for service in api relay exit-a exit-b; do
    echo "--- logs: ${service} (last 150 lines) ---"
    "${compose[@]}" logs --no-color --no-log-prefix --tail=150 "$service" 2>&1 | redact || true
  done
  echo '=== end of relay E2E diagnostics ==='
}
cleanup() {
  local status=$?
  trap - EXIT
  if ((status != 0)); then diagnose >&2 || true; fi
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$E2E_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$work"
  if ((status != 0)); then echo 'Relay E2E failed; redacted diagnostics above, no evidence retained' >&2; fi
  exit "$status"
}
trap cleanup EXIT
assert() { printf '{"kind":"assertion","name":"%s","status":"pass"}\n' "$1" >>"$work/assertions.jsonl"; }
policy() { "${compose[@]}" exec -T "$1" nft list table inet proxima_relay; }
wait_policy() {
  local service=$1 pattern=$2 expectation=$3
  for ((attempt=1;attempt<=45;attempt++)); do
    local rules
    rules=$(policy "$service")
    if [[ "$expectation" == present && "$rules" == *"$pattern"* ]] ||
       [[ "$expectation" == absent && "$rules" != *"$pattern"* ]]; then return; fi
    sleep 1
  done
  echo "policy reconciliation timed out: $service" >&2
  exit 1
}

echo 'Building isolated image (Xray release download permitted during build)'
docker build -q -t "$E2E_IMAGE" -f "${root}/tests/relay-e2e/Dockerfile" "$root" >/dev/null
"${compose[@]}" up -d --wait postgres redis api >/dev/null
E2E_ACTION=provision "${compose[@]}" run --rm -T control >/dev/null
"${compose[@]}" up -d --wait relay exit-a exit-b >/dev/null
for service in relay exit-a exit-b; do
  [[ $("${compose[@]}" exec -T "$service" cat /proc/1/comm) == node-agent ]] || exit 1
done
"${compose[@]}" exec -T exit-a busybox pidof xray >/dev/null
"${compose[@]}" exec -T exit-b busybox pidof xray >/dev/null
E2E_ACTION=subscription "${compose[@]}" run --rm -T control >/dev/null
assert agents_ack
assert profile_a
assert profile_b
entry_policy=$(policy relay)
[[ "$entry_policy" == *'24443'* && "$entry_policy" == *'24444'* &&
   "$entry_policy" == *"$EXIT_A_BACK_IP"* && "$entry_policy" == *"$EXIT_B_BACK_IP"* ]] || exit 1
assert topology
if "${compose[@]}" exec -T relay busybox pidof xray >/dev/null 2>&1; then exit 1; fi
[[ "$entry_policy" != *'udp dport map'* ]] || exit 1
[[ $("${compose[@]}" exec -T relay sh -c 'ls /state') == relay-agent.json ]] || exit 1
assert entry_forward_only
E2E_MODE=positive "${compose[@]}" run --rm -T client >/dev/null
for name in tcp_a tcp_b udp_a udp_b; do assert "$name"; done
echo 'Positive TCP/UDP probes passed; checking bracketed denials'
E2E_MODE=negative "${compose[@]}" run --rm -T client >/dev/null
for name in unused_port swapped_port_a swapped_port_b cross_credentials_a cross_credentials_b \
  uuid_swap_a uuid_swap_b direct_a direct_b; do assert "$name"; done

E2E_ACTION=legacy "${compose[@]}" run --rm -T control >/dev/null
echo 'Negative matrix passed; checking DB-seeded legacy compatibility'
wait_policy relay 24445 present
E2E_ACTION=fault "${compose[@]}" run --rm -T control >/dev/null
assert legacy_api_409
wait_policy relay 24445 absent
exit_rules=$(policy exit-a)
[[ "$exit_rules" == *'tcp dport 8443 drop'* ]] || exit 1
assert legacy_protected_drop
E2E_MODE=legacy-deny "${compose[@]}" run --rm -T client >/dev/null
assert legacy_fresh_denial
E2E_ACTION=restore "${compose[@]}" run --rm -T control >/dev/null
wait_policy relay 24445 present
E2E_MODE=recovery "${compose[@]}" run --rm -T client >/dev/null
assert legacy_recovery

before=$(policy relay)
relay_container=$("${compose[@]}" ps -q relay)
docker network disconnect "${project}_control" "$relay_container"
sleep 35
[[ $(policy relay) == "$before" ]] || exit 1
E2E_MODE=lkg "${compose[@]}" run --rm -T client >/dev/null
docker network connect "${project}_control" "$relay_container"
assert api_fetch_lkg
before=$(policy relay)
if printf 'flush table inet proxima_relay\nadd rule inet proxima_relay nonexistent accept\n' |
  "${compose[@]}" exec -T relay nft -f - >/dev/null 2>&1; then exit 1; fi
[[ $(policy relay) == "$before" ]] || exit 1
E2E_MODE=lkg "${compose[@]}" run --rm -T client >/dev/null
assert invalid_nft_lkg

printf '{"kind":"end","name":"complete","status":"pass"}\n' >>"$work/assertions.jsonl"
node "${root}/tests/relay-e2e/check-results.js" "$work/assertions.jsonl" >"$work/summary.json"
printf '{"entry_hostname":"entry.relay-e2e.test","entry_ports":[24443,24444],"exit_count":2,"exit_port":8443,"sni":"decoy.relay-e2e.test","legacy_fixture":"DB-seeded"}\n' >"$work/topology.json"
for service in relay exit-a exit-b; do policy "$service" >"$work/policy-${service}.nft"; done
mkdir -p "$(dirname "$output")"
[[ ! -e "$output" && ! -L "$output" ]] || exit 2
mv "$work" "$output"
work=$(mktemp -d)
echo "PASS: 24 assertions; sanitized evidence: $output"
