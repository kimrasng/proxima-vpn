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
source "${root}/tests/relay-e2e/topology.sh"
work=$(mktemp -d)
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
E2E_ACTION=apply "${compose[@]}" run --rm -T control >/dev/null
assert routes_applied
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
E2E_ACTION=traffic "${compose[@]}" run --rm -T control >/dev/null
assert route_traffic
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
echo "PASS: 26 assertions; sanitized evidence: $output"
