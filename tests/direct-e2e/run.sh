#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
compose=(docker compose -f "${root}/tests/direct-e2e/compose.yml")
project="direct-e2e-$PPID-$$"
octet=$((50 + ($$ % 150)))

export COMPOSE_PROJECT_NAME="${project}"
export E2E_IMAGE="direct-e2e:${project}"
export DIRECT_SUBNET="10.${octet}.31.0/24"
export EXIT_IP="10.${octet}.31.30"

cleanup() {
  status=$?
  if (( status != 0 )); then
    "${compose[@]}" ps || true
    "${compose[@]}" logs --no-color || true
  fi
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker image rm "${E2E_IMAGE}" >/dev/null 2>&1 || true
  exit "${status}"
}
trap cleanup EXIT

docker build -t "${E2E_IMAGE}" -f "${root}/tests/relay-e2e/Dockerfile" "${root}"
"${compose[@]}" up -d --wait postgres redis api
"${compose[@]}" run --rm control
"${compose[@]}" up -d --wait exit

exit_process=$("${compose[@]}" exec -T exit cat /proc/1/comm)
xray_pid=$("${compose[@]}" exec -T exit busybox pidof xray)
[[ "$exit_process" == node-agent && -n "$xray_pid" ]] || {
  echo 'FAIL: expected exit node-agent and Xray processes' >&2
  exit 1
}
echo "PASS: exit node-agent and Xray running (PID ${xray_pid})"

"${compose[@]}" run --rm client
echo 'PASS: direct VLESS Reality E2E completed without an entry relay'
