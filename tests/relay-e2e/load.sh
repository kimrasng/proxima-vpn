#!/usr/bin/env bash
# Entry -> Exit load benchmark on the disposable relay E2E topology.
#
#   bash tests/relay-e2e/load.sh [--output FILE] [loadgen bench flags...]
#
# Builds the same image and topology as run.sh (one Entry forwarding with
# nftables DNAT, two Exits running Xray Reality, a real control plane), then
# drives both routes through real client tunnels: single-stream TCP each way,
# connection setup and round-trip latency, paced UDP via SOCKS UDP ASSOCIATE,
# and every route's streams at once. It finally compares the Entry's per-route
# counters on the panel with the payload that crossed each tunnel.
#
# Everything runs on one Docker host, so the numbers describe this software
# path on this machine, not fleet capacity or a real WAN. It never touches a
# real node.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
output=""
if (( $# >= 2 )) && [[ "$1" == --output ]]; then
  output=$2
  shift 2
  [[ ! -e "$output" && ! -L "$output" ]] || { echo 'output path already exists' >&2; exit 2; }
fi
export E2E_LOAD_ARGS="$*"
source "${root}/tests/relay-e2e/topology.sh"
work=$(mktemp -d)
cleanup() {
  local status=$?
  trap - EXIT
  if ((status != 0)); then diagnose >&2 || true; fi
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$E2E_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

echo 'Building isolated image (Xray release download permitted during build)'
docker build -q -t "$E2E_IMAGE" -f "${root}/tests/relay-e2e/Dockerfile" "$root" >/dev/null
"${compose[@]}" up -d --wait postgres redis api >/dev/null
E2E_ACTION=provision "${compose[@]}" run --rm -T control >/dev/null
"${compose[@]}" up -d --wait relay exit-a exit-b >/dev/null
E2E_ACTION=subscription "${compose[@]}" run --rm -T control >/dev/null
E2E_ACTION=apply "${compose[@]}" run --rm -T control >/dev/null
echo 'Topology ready; running the benchmark (a few minutes)'

E2E_MODE=load "${compose[@]}" run --rm -T client >/dev/null
"${compose[@]}" run --rm -T --no-deps --entrypoint cat client /client-out/load.json >"$work/load.json"

# The Entry reports its counters in the next heartbeat (10 s). Each route's
# counted wire bytes must cover the payload that crossed its tunnel; they
# exceed it by Reality/TLS, VLESS, TCP and IP overhead and by the probes.
E2E_ACTION=load_traffic "${compose[@]}" run --rm -T control >"$work/route-traffic.json"

node - "$work/load.json" "$work/route-traffic.json" >"$work/report.json" <<'JS'
const fs = require('node:fs');
const load = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const counted = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const failures = [...load.failures];
const accounting = {};
for (const [name, route] of Object.entries(load.routes)) {
  const c = counted[name] || { up_bytes: 0, down_bytes: 0 };
  const ratio = (wire, payload) => payload > 0 ? Number((wire / payload).toFixed(3)) : null;
  accounting[name] = {
    payload_up_bytes: route.payload_up_bytes, payload_down_bytes: route.payload_down_bytes,
    entry_counted_up_bytes: c.up_bytes, entry_counted_down_bytes: c.down_bytes,
    up_overhead_ratio: ratio(c.up_bytes, route.payload_up_bytes),
    down_overhead_ratio: ratio(c.down_bytes, route.payload_down_bytes),
  };
  if (c.up_bytes < route.payload_up_bytes || c.down_bytes < route.payload_down_bytes)
    failures.push(`${name}: Entry counted less than the tunnel payload`);
}
const report = { ...load, entry_route_accounting: accounting, failures };
process.stdout.write(JSON.stringify(report, null, 2) + '\n');
JS

node - "$work/report.json" <<'JS'
const r = JSON.parse(require('node:fs').readFileSync(process.argv[2], 'utf8'));
const row = (...cells) => console.log(cells.map((c, i) => String(c).padEnd(i ? 18 : 24)).join(''));
row('', ...Object.keys(r.routes));
const each = (label, pick) => row(label, ...Object.values(r.routes).map(pick));
each('TCP down (Mbps)', x => x.tcp_down_mbps.toFixed(1));
each('TCP up (Mbps)', x => x.tcp_up_mbps.toFixed(1));
each('first response p50/p95', x => `${x.first_response_ms.p50}/${x.first_response_ms.p95} ms`);
each('opaque first p50/p95', x => `${x.opaque_first_response_ms.p50}/${x.opaque_first_response_ms.p95} ms`);
each('RTT p50/p95', x => `${x.rtt_ms.p50}/${x.rtt_ms.p95} ms`);
each('UDP loss', x => `${x.udp.loss_percent.toFixed(2)}% @${x.udp.offered_pps}pps`);
each('UDP RTT p50/p95', x => `${x.udp.rtt_ms.p50}/${x.udp.rtt_ms.p95} ms`);
each('Entry wire/payload', (x, i) => {
  const a = r.entry_route_accounting[Object.keys(r.routes)[i]];
  return `${a.up_overhead_ratio}/${a.down_overhead_ratio}`;
});
const c = r.concurrent;
console.log(`concurrent: ${c.completed}/${c.streams} streams x ${c.stream_mib} MiB in ${c.seconds}s = ${c.aggregate_down_mbps} Mbps aggregate`);
if (r.failures.length) console.log('FAILURES:\n  ' + r.failures.join('\n  '));
JS

if [[ -n "$output" ]]; then
  mkdir -p "$(dirname "$output")"
  cp "$work/report.json" "$output"
  echo "report: $output"
fi
# Threshold misses fail the run only after the measurements are printed.
[[ $(jq '.failures | length' "$work/report.json") == 0 ]]
