# Shared by run.sh (correctness E2E) and load.sh (benchmark): one disposable
# Compose project with an Entry and two Exits on random private subnets.
# Source it after setting root.

project="relay-e2e-$(openssl rand -hex 5)"
octet=$(( 100 + 0x$(openssl rand -hex 1) % 100 ))
third=$(( 20 + 0x$(openssl rand -hex 1) % 200 ))
export COMPOSE_PROJECT_NAME=$project E2E_IMAGE="relay-e2e:${project}"
export FRONT_SUBNET="10.${octet}.${third}.0/24" BACK_SUBNET="10.${octet}.$((third+1)).0/24"
export CLIENT_FRONT_IP="10.${octet}.${third}.10" RELAY_FRONT_IP="10.${octet}.${third}.20"
export CLIENT_BACK_IP="10.${octet}.$((third+1)).10" RELAY_BACK_IP="10.${octet}.$((third+1)).20"
export EXIT_A_BACK_IP="10.${octet}.$((third+1)).30" EXIT_B_BACK_IP="10.${octet}.$((third+1)).40"
compose=(docker compose -f "${root}/tests/relay-e2e/compose.yml")
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
# Stateless (-s): the Entry's per-route counters change with traffic and are
# reset by every heartbeat, and policy comparisons are about rules only.
policy() { "${compose[@]}" exec -T "$1" nft -s list table inet proxima_relay; }
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
