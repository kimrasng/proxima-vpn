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
export CAP_FIRST_IP="10.${octet}.31.41"
export CAP_SECOND_IP="10.${octet}.31.42"

cleanup() {
  status=$?
  # KEEP_E2E=1 leaves the stack up for debugging; remove it with `docker compose -p <project> down -v`.
  if [[ -n "${KEEP_E2E:-}" ]]; then
    echo "KEEP_E2E set: leaving compose project ${project} running (exit ${status})"
    exit "${status}"
  fi
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
"${compose[@]}" run --rm publish

exit_process=$("${compose[@]}" exec -T exit cat /proc/1/comm)
xray_pid=$("${compose[@]}" exec -T exit busybox pidof xray)
[[ "$exit_process" == node-agent && -n "$xray_pid" ]] || {
  echo 'FAIL: expected exit node-agent and Xray processes' >&2
  exit 1
}
echo "PASS: exit node-agent and Xray running (PID ${xray_pid})"

"${compose[@]}" run --rm client
echo 'PASS: direct VLESS Reality E2E completed without an entry relay'

# --- Account cap enforcement -------------------------------------------------
# Needs a kernel with CONFIG_INET_DIAG_DESTROY and CAP_NET_ADMIN on the Exit.
# Docker Desktop's kernel lacks the option, so the stage is skipped (loudly)
# there unless CAP_E2E=1 forces it. CI (ubuntu-latest) and a Linux VM run it.
#
# Capability is read from the Exit agent's own startup probe, which really
# destroys a socket. Kernel config files are not visible inside containers, so
# they cannot be used to decide this.
cap_supported() {
  "${compose[@]}" logs exit 2>/dev/null | grep -q 'client address limit: connection closing is available'
}
if [[ "${CAP_E2E:-auto}" == 0 ]]; then
  echo 'SKIP: account cap enforcement stage disabled (CAP_E2E=0)'
  exit 0
fi
if [[ "${CAP_E2E:-auto}" != 1 ]] && ! cap_supported; then
  echo 'SKIP: this kernel lacks CONFIG_INET_DIAG_DESTROY; account cap enforcement stage not run'
  exit 0
fi
if "${compose[@]}" logs exit 2>/dev/null | grep -q 'client address limit cannot be enforced'; then
  echo 'FAIL: exit agent reports it cannot close connections on this host' >&2
  exit 1
fi
echo 'PASS: exit agent reports connection closing is available'

hold=${CAP_HOLD_SECONDS:-170}
export CAP_HOLD_SECONDS="${hold}"
first_log=$(mktemp); second_log=$(mktemp)

# The first client connects and holds a tunnel open.
"${compose[@]}" run --rm -T cap-first >"${first_log}" 2>&1 &
first_pid=$!
for _ in $(seq 1 60); do
  grep -q 'PASS\[first\]' "${first_log}" 2>/dev/null && break
  sleep 1
done
grep -q 'PASS\[first\]' "${first_log}" || { cat "${first_log}" >&2; echo 'FAIL: first client never established its tunnel' >&2; exit 1; }
echo 'PASS: first client (older address) is connected and holding a tunnel'

# The second client connects from another address with the same shared UUID.
"${compose[@]}" run --rm -T cap-second >"${second_log}" 2>&1 &
second_pid=$!
for _ in $(seq 1 60); do
  grep -q 'PASS\[second\]' "${second_log}" 2>/dev/null && break
  sleep 1
done
grep -q 'PASS\[second\]' "${second_log}" || { cat "${second_log}" >&2; echo 'FAIL: second client never established its tunnel' >&2; exit 1; }
echo 'PASS: second client (newer address, same UUID) connected before enforcement'

# The scheduler sweeps every 10s, evaluates each account every 30s and acts
# after 2 consecutive over-cap sweeps; allow generous slack.
deadline=$((SECONDS + 150))
while (( SECONDS < deadline )); do
  grep -q 'HOLD_RESULT' "${second_log}" 2>/dev/null && break
  sleep 2
done
grep -q 'HOLD_RESULT' "${second_log}" || {
  echo '--- second client log'; cat "${second_log}" >&2
  echo 'FAIL: the newer address was never disconnected by the account cap' >&2
  exit 1
}
second_elapsed=$(sed -n 's/.*role=second .*elapsed=\([0-9]*\)s.*/\1/p' "${second_log}" | tail -1)
second_exit=$(sed -n 's/.*role=second curl_exit=\([0-9]*\) .*/\1/p' "${second_log}" | tail -1)
(( second_elapsed < hold - 10 )) || {
  echo "FAIL: second client held its tunnel for the whole ${hold}s; it was not cut off" >&2
  exit 1
}
echo "PASS: newer address was disconnected by the account cap after ${second_elapsed}s (curl exit ${second_exit})"

# The older address must survive: its transfer is still running.
if grep -q 'HOLD_RESULT' "${first_log}" 2>/dev/null; then
  first_elapsed=$(sed -n 's/.*role=first .*elapsed=\([0-9]*\)s.*/\1/p' "${first_log}" | tail -1)
  if (( first_elapsed < second_elapsed + 5 )); then
    echo "FAIL: the older address was also disconnected (after ${first_elapsed}s)" >&2
    exit 1
  fi
fi
kill -0 "${first_pid}" 2>/dev/null || {
  cat "${first_log}" >&2
  echo 'FAIL: the older address lost its tunnel; only the newer one may be cut off' >&2
  exit 1
}
echo 'PASS: older address kept its tunnel while the newer one was cut off'

# Enforcement closes transports; it must never revoke the shared credential.
"${compose[@]}" run --rm client >/dev/null 2>&1 || {
  echo 'FAIL: the shared UUID stopped working for new connections after enforcement' >&2
  exit 1
}
echo 'PASS: shared UUID still works for new connections (no credential revocation)'

kill "${first_pid}" "${second_pid}" 2>/dev/null || true
wait "${first_pid}" "${second_pid}" 2>/dev/null || true
rm -f "${first_log}" "${second_log}"
echo 'PASS: account cap enforcement E2E completed'
