#!/usr/bin/env bash
# Full data-plane end-to-end test: registers a real node-agent against a real
# api-server, configures a VLESS Reality inbound exactly the way an admin would
# through the panel API, starts the real xray-core that results, and then acts
# as a real client - proving actual encrypted traffic reaches a local HTTP
# target through the tunnel, not just that config files parse. Then it exercises
# the agent's live-management paths against that running Xray: adding a device
# mid-flight must be applied over the handler API without a restart, killing
# Xray must be noticed and repaired by the supervisor, and the bytes those
# tunnels moved must land in users.traffic_used and then actually gate access
# once the cap is exceeded. Finishes by running `node-agent unregister` (the
# same command scripts/uninstall.sh runs) and confirming the node is actually
# gone from the panel and its API key is rejected afterward.
#
# The VMess/Trojan/Shadowsocks/WireGuard legs are retained but off by default:
# a node serves one protocol now, so they cannot share this node. Run them with
# E2E_LEGACY_PROTOCOLS=1 against a build without that constraint.
#
# This intentionally does NOT exercise ACME/Let's Encrypt (IssueCertificate):
# that requires a real public domain reachable from the internet on port 80,
# which no CI environment can provide. Instead it calls the node's own
# ReportTLSCert endpoint directly with a self-signed cert, simulating what a
# successful ACME run would have reported, so VMess/Trojan (which require a
# node-level TLS cert - see xray_config.go buildVmessWS/buildTrojanTLS) can
# still be tested end to end. The IssueCertificate -> GetTLSDomain leg (the
# part that previously silently did nothing) is still asserted separately.
#
# Expects on PATH: curl, jq, openssl, python3, xray, wg, wg-quick, sudo (for
# wg-quick/xray-as-root - Xray doesn't strictly need root, but wg-quick and
# `tc` do, and node-agent manages both from the same process).
#
# Required env:
#   NODE_AGENT_BIN    path to a built node-agent binary
#   BASE_URL          e.g. http://localhost:2053 (api-server must already be
#                     running and reachable here, with an admin seeded)
#   ADMIN_EMAIL / ADMIN_PASSWORD
#
# Exit code is non-zero on any assertion failure. On failure, relevant logs
# are dumped to stdout so they show up in the CI job log without needing a
# separate artifact-download step (a companion artifact upload is still
# wired up in the workflow for the full files).

set -euo pipefail

: "${NODE_AGENT_BIN:?}"
: "${BASE_URL:?}"
: "${ADMIN_EMAIL:?}"
: "${ADMIN_PASSWORD:?}"

# A node now serves one protocol (AdminInboundHandler.Create plus a unique index
# on inbounds.node_id), so the VMess/Trojan/Shadowsocks/WireGuard legs can no
# longer share this node. Kept, not deleted - they are the only data-plane
# coverage those protocols have - and run with E2E_LEGACY_PROTOCOLS=1.
LEGACY_PROTOCOLS="${E2E_LEGACY_PROTOCOLS:-0}"

WORKDIR=$(mktemp -d)
LOGDIR="${E2E_LOG_DIR:-$WORKDIR/logs}"
mkdir -p "$LOGDIR"

PIDS=()
cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    echo ""
    echo "=== FAILURE: dumping logs ==="
    for f in "$LOGDIR"/*.log; do
      [[ -f "$f" ]] || continue
      echo "--- $f ---"
      tail -n 80 "$f" || true
      echo ""
    done
  fi
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  sudo wg-quick down wgc0 >/dev/null 2>&1 || true
  exit $status
}
trap cleanup EXIT

log()  { echo "[e2e] $*"; }
fail() { echo "[e2e] FAIL: $*"; exit 1; }

wait_for() {
  # wait_for <description> <timeout_seconds> <command...>
  local desc="$1" timeout="$2"; shift 2
  local waited=0
  until "$@" >/dev/null 2>&1; do
    waited=$((waited + 1))
    if [[ $waited -ge $timeout ]]; then
      fail "timed out waiting for: $desc"
    fi
    sleep 1
  done
  log "ready: $desc (${waited}s)"
}

assert_eq() {
  local desc="$1" expected="$2" actual="$3"
  if [[ "$expected" != "$actual" ]]; then
    fail "$desc — expected [$expected], got [$actual]"
  fi
  log "PASS: $desc"
}

# ---------------------------------------------------------------------------
# 1. Admin login
# ---------------------------------------------------------------------------
log "logging in as admin"
ADMIN_TOKEN=$(curl -sf -X POST "$BASE_URL/api/v1/admin/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" | jq -r .token)
[[ -n "$ADMIN_TOKEN" && "$ADMIN_TOKEN" != "null" ]] || fail "admin login did not return a token"

# ---------------------------------------------------------------------------
# 2. Register a node directly against the API (bypassing `node-agent
#    register`'s IP auto-detection so the node's address is deterministically
#    127.0.0.1 - the server and client below both run on this same host).
# ---------------------------------------------------------------------------
log "generating registration token"
REG_TOKEN=$(curl -sf -X POST "$BASE_URL/api/v1/admin/nodes/token" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq -r .token)

log "registering node"
REG_RESP=$(curl -sf -X POST "$BASE_URL/api/v1/nodes/register" \
  -H "Content-Type: application/json" \
  -d "{\"reg_token\":\"$REG_TOKEN\",\"ip\":\"127.0.0.1\",\"port\":8443,\"xray_version\":\"$(xray version | head -1 | awk '{print $2}')\",\"name\":\"e2e-node\",\"country\":\"US\",\"region\":\"e2e\"}")
NODE_ID=$(echo "$REG_RESP" | jq -r .node_id)
NODE_KEY=$(echo "$REG_RESP" | jq -r .api_key)
[[ -n "$NODE_ID" && "$NODE_ID" != "null" ]] || fail "node registration did not return node_id"

# ---------------------------------------------------------------------------
# 3. Create one inbound per protocol, matching what an admin would submit via
#    NodeInbounds.tsx. Shadowsocks uses a real base64 PSK (not an arbitrary
#    string) - xray-core's 2022-* ciphers hard-require this; a plain-text
#    password crashes the whole Xray process (and every other protocol on
#    the same node with it), which is exactly the kind of regression this
#    test exists to catch.
# ---------------------------------------------------------------------------
VLESS_PORT=8443
VMESS_PORT=8444
TROJAN_PORT=8445
SS_PORT=8446
WG_PORT=58120

log "creating inbounds"
create_inbound() {
  curl -sf -X POST "$BASE_URL/api/v1/admin/nodes/$NODE_ID/inbounds" \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    -d "$1"
}

create_inbound "{\"protocol\":\"vless_reality\",\"port\":$VLESS_PORT,\"tag\":\"vless-in\",\"settings\":{\"dest\":\"www.cloudflare.com:443\",\"server_names\":[\"www.cloudflare.com\"]}}" > "$WORKDIR/ib_vless.json"
if [[ "$LEGACY_PROTOCOLS" == "1" ]]; then
create_inbound "{\"protocol\":\"vmess_ws\",\"port\":$VMESS_PORT,\"tag\":\"vmess-in\",\"settings\":{\"ws_path\":\"/vmess\"}}" > "$WORKDIR/ib_vmess.json"
create_inbound "{\"protocol\":\"trojan_tls\",\"port\":$TROJAN_PORT,\"tag\":\"trojan-in\",\"settings\":{}}" > "$WORKDIR/ib_trojan.json"

SS_PSK=$(openssl rand -base64 16)
create_inbound "{\"protocol\":\"shadowsocks\",\"port\":$SS_PORT,\"tag\":\"ss-in\",\"settings\":{\"password\":\"$SS_PSK\",\"method\":\"2022-blake3-aes-128-gcm\"}}" > "$WORKDIR/ib_ss.json"

create_inbound "{\"protocol\":\"wireguard\",\"port\":$WG_PORT,\"tag\":\"wg-in\",\"settings\":{}}" > "$WORKDIR/ib_wg.json"
WG_SERVER_PRIVKEY=$(jq -r .settings.private_key "$WORKDIR/ib_wg.json")
WG_SERVER_PUBKEY=$(echo "$WG_SERVER_PRIVKEY" | wg pubkey)
else
log "SKIP: non-VLESS inbounds (a node serves one protocol; set E2E_LEGACY_PROTOCOLS=1)"
fi

# ---------------------------------------------------------------------------
# 4. TLS: exercise the admin -> node-agent domain-request leg for real (this
#    is exactly the chain that used to silently do nothing - see
#    IssueCertificate/GetTLSDomain), then inject a self-signed cert via
#    ReportTLSCert to stand in for what a real ACME success would report,
#    since actual ACME needs a real public domain.
# ---------------------------------------------------------------------------
log "asserting IssueCertificate -> GetTLSDomain wiring"
curl -sf -X POST "$BASE_URL/api/v1/admin/nodes/$NODE_ID/tls/issue" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"domain":"e2e-test.example.com","email":"e2e@example.com"}' > /dev/null

GOT_DOMAIN=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/tls-domain" -H "X-Node-Key: $NODE_KEY" | jq -r .domain)
assert_eq "node can read back the admin-requested TLS domain" "e2e-test.example.com" "$GOT_DOMAIN"

log "generating self-signed cert (stand-in for a completed ACME issuance) and reporting it"
mkdir -p "$WORKDIR/certs"
openssl req -x509 -newkey rsa:2048 -keyout "$WORKDIR/certs/self.key" -out "$WORKDIR/certs/self.crt" \
  -days 1 -nodes -subj "/CN=e2e-test.local" -addext "subjectAltName=DNS:e2e-test.local" >/dev/null 2>&1
sudo cp "$WORKDIR/certs/self.crt" /usr/local/share/ca-certificates/e2e-test.crt
sudo update-ca-certificates >/dev/null

curl -sf -X POST "$BASE_URL/api/v1/nodes/$NODE_ID/tls-cert" \
  -H "X-Node-Key: $NODE_KEY" -H "Content-Type: application/json" \
  -d "{\"cert_file\":\"$WORKDIR/certs/self.crt\",\"key_file\":\"$WORKDIR/certs/self.key\"}" > /dev/null

HAS_CERT=$(curl -sf "$BASE_URL/api/v1/admin/nodes/$NODE_ID/tls" -H "Authorization: Bearer $ADMIN_TOKEN" | jq -r .has_cert)
assert_eq "node-reported cert is recorded" "true" "$HAS_CERT"

# ---------------------------------------------------------------------------
# 5. Node group + plan (no traffic_limit key at all - sending 0 means "0
#    bytes allowed", not unlimited; omitting it is how the real admin UI
#    represents unlimited) + user + device, so the node's generated Xray
#    config actually has a client, not an empty clients:[] that would make
#    every protocol reject with "invalid request user id". Done before
#    starting node-agent below so its very first config fetch already has
#    everything - no need to wait out a 30s config-poll cycle.
# ---------------------------------------------------------------------------
log "creating node group / plan / user / device"
NG_ID=$(curl -sf -X POST "$BASE_URL/api/v1/admin/node-groups/" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"e2e-group"}' | jq -r .id)
curl -sf -X PUT "$BASE_URL/api/v1/admin/node-groups/$NG_ID/nodes" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"node_ids\":[\"$NODE_ID\"]}" > /dev/null

PLAN_ID=$(curl -sf -X POST "$BASE_URL/api/v1/admin/plans/" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"name\":\"e2e-plan\",\"node_group_id\":\"$NG_ID\",\"duration_days\":30,\"max_devices\":3,\"is_active\":true}" | jq -r .id)

USER_EMAIL="e2e-user@example.com"
USER_PASSWORD="E2EUserPass123!"
USER_ID=$(curl -sf -X POST "$BASE_URL/api/v1/admin/users/" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"email\":\"$USER_EMAIL\",\"password\":\"$USER_PASSWORD\",\"name\":\"e2e user\",\"is_active\":true}" | jq -r .id)
curl -sf -X PUT "$BASE_URL/api/v1/admin/users/$USER_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"plan_id\":\"$PLAN_ID\",\"status\":\"active\",\"is_active\":true}" > /dev/null

USER_TOKEN=$(curl -sf -X POST "$BASE_URL/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$USER_EMAIL\",\"password\":\"$USER_PASSWORD\"}" | jq -r .token)

DEVICE=$(curl -sf -X POST "$BASE_URL/api/v1/user/devices" \
  -H "Authorization: Bearer $USER_TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"e2e-device"}')
DEVICE_ID=$(echo "$DEVICE" | jq -r .id)
CLIENT_UUID=$(echo "$DEVICE" | jq -r .xray_uuid)
CLIENT_WG_ADDRESS=$(echo "$DEVICE" | jq -r .wg_address)
[[ -n "$CLIENT_UUID" && "$CLIENT_UUID" != "null" ]] || fail "device creation did not return xray_uuid"

# ---------------------------------------------------------------------------
# 6. Start the real node-agent (and, transitively, real xray-core + wg-quick)
# ---------------------------------------------------------------------------
log "writing node-agent config and starting node-agent (real xray-core underneath)"
cat > "$WORKDIR/node-agent-config.json" <<EOF
{"node_id":"$NODE_ID","api_key":"$NODE_KEY","server_url":"$BASE_URL"}
EOF

sudo "$NODE_AGENT_BIN" run --config "$WORKDIR/node-agent-config.json" \
  > "$LOGDIR/node-agent.log" 2>&1 &
PIDS+=($!)

wait_for "vless port $VLESS_PORT listening" 30 bash -c "ss -tln | grep -q ':$VLESS_PORT '"
if [[ "$LEGACY_PROTOCOLS" == "1" ]]; then
wait_for "vmess port $VMESS_PORT listening" 10 bash -c "ss -tln | grep -q ':$VMESS_PORT '"
wait_for "trojan port $TROJAN_PORT listening" 10 bash -c "ss -tln | grep -q ':$TROJAN_PORT '"
wait_for "shadowsocks port $SS_PORT listening" 10 bash -c "ss -tln | grep -q ':$SS_PORT '"
wait_for "wireguard interface wg0 up" 30 bash -c "ip link show wg0"
fi

if grep -qi "Failed to start" "$LOGDIR/node-agent.log"; then
  fail "xray-core failed to start (see node-agent.log above) - likely a config generation bug"
fi

# ---------------------------------------------------------------------------
# 7. Wait for the node's first real heartbeat (fires every 30s - see
#    heartbeatLoop in node-agent/cmd/main.go) to flip status to 'online'.
#    This both proves the heartbeat path works end to end and unblocks the
#    subscription endpoint below, which excludes status='pending' nodes.
# ---------------------------------------------------------------------------
log "waiting for node-agent's first heartbeat to mark the node online"
waited=0
while true; do
  STATUS=$(curl -sf "$BASE_URL/api/v1/admin/nodes/$NODE_ID" -H "Authorization: Bearer $ADMIN_TOKEN" | jq -r .status)
  [[ "$STATUS" == "online" ]] && break
  waited=$((waited + 1))
  [[ $waited -ge 45 ]] && fail "node status never reached 'online' (still '$STATUS' after 45s) - heartbeat is not working"
  sleep 1
done
log "PASS: node status = online (${waited}s, via real heartbeat)"

# ---------------------------------------------------------------------------
# 8. Pull the client-side secrets no admin/user API exposes directly (by
#    design) - wg_private_key and the Reality public key/short id are only
#    ever surfaced through the subscription endpoint, so fetch them exactly
#    the way a real client/device would.
# ---------------------------------------------------------------------------
log "fetching client credentials via the subscription endpoint"
SUB_TOKEN=$(curl -sf -X POST "$BASE_URL/api/v1/user/sub-token/regenerate" \
  -H "Authorization: Bearer $USER_TOKEN" | jq -r .sub_token)

if [[ "$LEGACY_PROTOCOLS" == "1" ]]; then
WG_CONF=$(curl -sf "$BASE_URL/sub/$SUB_TOKEN/$DEVICE_ID?format=wireguard")
CLIENT_WG_PRIVKEY=$(echo "$WG_CONF" | sed -n 's/^PrivateKey = //p' | head -1)
[[ -n "$CLIENT_WG_PRIVKEY" ]] || fail "wireguard subscription did not include a PrivateKey line"
fi

SUB_V2RAY=$(curl -sf "$BASE_URL/sub/$SUB_TOKEN/$DEVICE_ID" | base64 -d)
VLESS_LINK=$(echo "$SUB_V2RAY" | grep '^vless://' | head -1)
[[ -n "$VLESS_LINK" ]] || fail "default subscription did not include a vless:// link"
REALITY_PUBKEY=$(echo "$VLESS_LINK" | grep -oP '(?<=pbk=)[^&]+' | python3 -c "import sys,urllib.parse; print(urllib.parse.unquote(sys.stdin.read().strip()))")
REALITY_SHORTID=$(echo "$VLESS_LINK" | grep -oP '(?<=sid=)[^&]+' | python3 -c "import sys,urllib.parse; print(urllib.parse.unquote(sys.stdin.read().strip()))")
[[ -n "$REALITY_PUBKEY" && -n "$REALITY_SHORTID" ]] || fail "could not extract reality pbk/sid from vless link"

# ---------------------------------------------------------------------------
# 9. Local HTTP target, standing in for "the internet" so this test has no
#    external network dependency. Freedom/direct outbounds on the server
#    reach it over loopback exactly like they'd reach any real destination.
# ---------------------------------------------------------------------------
log "starting local HTTP target"
MARKER="e2e-$(openssl rand -hex 8)"
mkdir -p "$WORKDIR/www"
echo -n "$MARKER" > "$WORKDIR/www/marker.txt"
python3 -m http.server 9090 --directory "$WORKDIR/www" > "$LOGDIR/http-target.log" 2>&1 &
PIDS+=($!)
wait_for "local HTTP target" 10 curl -sf http://127.0.0.1:9090/marker.txt

fetch_via_socks() {
  curl -s --max-time 10 -x "socks5h://127.0.0.1:$1" http://127.0.0.1:9090/marker.txt
}

# ---------------------------------------------------------------------------
# 10. VLESS Reality: real client, real Reality handshake
# ---------------------------------------------------------------------------
log "testing VLESS Reality"
cat > "$WORKDIR/vless-client.json" <<EOF
{
  "inbounds": [{"listen":"127.0.0.1","port":1080,"protocol":"socks","settings":{"udp":true}}],
  "outbounds": [{
    "protocol": "vless",
    "settings": {"vnext":[{"address":"127.0.0.1","port":$VLESS_PORT,"users":[{"id":"$CLIENT_UUID","flow":"xtls-rprx-vision","encryption":"none"}]}]},
    "streamSettings": {"network":"tcp","security":"reality","realitySettings":{"serverName":"www.cloudflare.com","fingerprint":"chrome","publicKey":"$REALITY_PUBKEY","shortId":"$REALITY_SHORTID","spiderX":""}}
  }]
}
EOF
xray -config "$WORKDIR/vless-client.json" > "$LOGDIR/vless-client.log" 2>&1 &
PIDS+=($!)
wait_for "vless client socks ready" 10 bash -c "ss -tln | grep -q ':1080 '"
assert_eq "VLESS Reality tunnel reaches target" "$MARKER" "$(fetch_via_socks 1080)"

# ---------------------------------------------------------------------------
# 11-14. VMess / Trojan / Shadowsocks / WireGuard data plane. DEPRECATED: a
#        node serves one protocol, so these cannot run beside the VLESS
#        inbound above. Retained because nothing else covers their data
#        plane. Not indented, to keep the heredoc terminators valid.
# ---------------------------------------------------------------------------
if [[ "$LEGACY_PROTOCOLS" == "1" ]]; then
# ---------------------------------------------------------------------------
# 11. VMess
# ---------------------------------------------------------------------------
log "testing VMess"
cat > "$WORKDIR/vmess-client.json" <<EOF
{
  "inbounds": [{"listen":"127.0.0.1","port":1081,"protocol":"socks","settings":{"udp":true}}],
  "outbounds": [{
    "protocol": "vmess",
    "settings": {"vnext":[{"address":"127.0.0.1","port":$VMESS_PORT,"users":[{"id":"$CLIENT_UUID","alterId":0}]}]},
    "streamSettings": {"network":"ws","security":"tls","wsSettings":{"path":"/vmess"},"tlsSettings":{"serverName":"e2e-test.local"}}
  }]
}
EOF
xray -config "$WORKDIR/vmess-client.json" > "$LOGDIR/vmess-client.log" 2>&1 &
PIDS+=($!)
wait_for "vmess client socks ready" 10 bash -c "ss -tln | grep -q ':1081 '"
assert_eq "VMess tunnel reaches target" "$MARKER" "$(fetch_via_socks 1081)"

# ---------------------------------------------------------------------------
# 12. Trojan
# ---------------------------------------------------------------------------
log "testing Trojan"
cat > "$WORKDIR/trojan-client.json" <<EOF
{
  "inbounds": [{"listen":"127.0.0.1","port":1082,"protocol":"socks","settings":{"udp":true}}],
  "outbounds": [{
    "protocol": "trojan",
    "settings": {"servers":[{"address":"127.0.0.1","port":$TROJAN_PORT,"password":"$CLIENT_UUID"}]},
    "streamSettings": {"network":"tcp","security":"tls","tlsSettings":{"serverName":"e2e-test.local"}}
  }]
}
EOF
xray -config "$WORKDIR/trojan-client.json" > "$LOGDIR/trojan-client.log" 2>&1 &
PIDS+=($!)
wait_for "trojan client socks ready" 10 bash -c "ss -tln | grep -q ':1082 '"
assert_eq "Trojan tunnel reaches target" "$MARKER" "$(fetch_via_socks 1082)"

# ---------------------------------------------------------------------------
# 13. Shadowsocks
# ---------------------------------------------------------------------------
log "testing Shadowsocks"
cat > "$WORKDIR/ss-client.json" <<EOF
{
  "inbounds": [{"listen":"127.0.0.1","port":1083,"protocol":"socks","settings":{"udp":true}}],
  "outbounds": [{
    "protocol": "shadowsocks",
    "settings": {"servers":[{"address":"127.0.0.1","port":$SS_PORT,"method":"2022-blake3-aes-128-gcm","password":"$SS_PSK"}]}
  }]
}
EOF
xray -config "$WORKDIR/ss-client.json" > "$LOGDIR/ss-client.log" 2>&1 &
PIDS+=($!)
wait_for "shadowsocks client socks ready" 10 bash -c "ss -tln | grep -q ':1083 '"
assert_eq "Shadowsocks tunnel reaches target" "$MARKER" "$(fetch_via_socks 1083)"

# ---------------------------------------------------------------------------
# 14. WireGuard: real Noise handshake + real data plane, via a client
#     interface named differently from the server's hardcoded "wg0" so both
#     can coexist in this single network namespace.
# ---------------------------------------------------------------------------
log "testing WireGuard"
sudo mkdir -p /etc/wireguard
sudo tee /etc/wireguard/wgc0.conf > /dev/null <<EOF
[Interface]
PrivateKey = $CLIENT_WG_PRIVKEY
Address = $CLIENT_WG_ADDRESS

[Peer]
PublicKey = $WG_SERVER_PUBKEY
Endpoint = 127.0.0.1:$WG_PORT
AllowedIPs = 10.66.0.1/32
PersistentKeepalive = 5
EOF
# AllowedIPs is deliberately the server's single WG address (/32), not its
# whole /16 pool (what a real client's AllowedIPs would use): server and
# client run in the same network namespace here, and the server's own wg0
# already owns the 10.66.0.0/16 route from its /16 address, so a second
# identical route via wgc0 fails with "RTNETLINK answers: File exists". A
# real deployment never hits this - the client is a different host.
sudo wg-quick up wgc0 > "$LOGDIR/wg-client.log" 2>&1

wait_for "wireguard handshake completed" 15 bash -c "sudo wg show wgc0 | grep -q 'latest handshake'"
log "PASS: WireGuard handshake completed"

assert_eq "WireGuard tunnel reaches target" "$MARKER" "$(curl -s --max-time 10 http://10.66.0.1:9090/marker.txt)"

else
log "SKIP: VMess/Trojan/Shadowsocks/WireGuard tunnels (deprecated, one protocol per node)"
fi

echo ""
log "DATA PLANE PASSED (VLESS Reality; legacy protocols gated by E2E_LEGACY_PROTOCOLS)"

# ---------------------------------------------------------------------------
# 14b. Live user sync over Xray's handler API. Adding a device used to require
#      restarting Xray, dropping every connected user; the agent now diffs the
#      config digest and, when only the user set changed, applies the delta via
#      AlterInbound instead. Proving that needs a real Xray: the protobuf
#      encoding is hand-rolled (see internal/xray/handler.go), so unit tests
#      can pin the bytes but only Xray itself can confirm it accepts them.
#
#      The assertion is that a device created *after* Xray started can connect
#      without Xray having restarted - checked both ways, since a restart would
#      also (eventually) make the new UUID work and would otherwise pass
#      silently.
# ---------------------------------------------------------------------------
log "testing live user sync (AlterInbound, no restart)"

XRAY_PID_BEFORE=$(pgrep -f "xray -config /etc/node-agent" | head -1)
[[ -n "$XRAY_PID_BEFORE" ]] || fail "could not find the server-side xray process"

DEVICE2=$(curl -sf -X POST "$BASE_URL/api/v1/user/devices" \
  -H "Authorization: Bearer $USER_TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"e2e-device-live"}')
DEVICE2_UUID=$(echo "$DEVICE2" | jq -r .xray_uuid)
[[ -n "$DEVICE2_UUID" && "$DEVICE2_UUID" != "null" ]] || fail "second device creation did not return xray_uuid"

# The agent polls the digest every 30s (configPollLoop), so allow one full
# cycle plus margin for the gRPC round-trip.
log "waiting for the agent to pick up the new user (digest poll is 30s)"
cat > "$WORKDIR/vless-client2.json" <<EOF
{
  "inbounds": [{"listen":"127.0.0.1","port":1084,"protocol":"socks","settings":{"udp":true}}],
  "outbounds": [{
    "protocol": "vless",
    "settings": {"vnext":[{"address":"127.0.0.1","port":$VLESS_PORT,"users":[{"id":"$DEVICE2_UUID","flow":"xtls-rprx-vision","encryption":"none"}]}]},
    "streamSettings": {"network":"tcp","security":"reality","realitySettings":{"serverName":"www.cloudflare.com","fingerprint":"chrome","publicKey":"$REALITY_PUBKEY","shortId":"$REALITY_SHORTID","spiderX":""}}
  }]
}
EOF
xray -config "$WORKDIR/vless-client2.json" > "$LOGDIR/vless-client2.log" 2>&1 &
PIDS+=($!)
wait_for "second vless client socks ready" 10 bash -c "ss -tln | grep -q ':1084 '"

# Retry rather than sleeping a flat 45s: succeeds as soon as the sync lands.
waited=0
until [[ "$(fetch_via_socks 1084)" == "$MARKER" ]]; do
  waited=$((waited + 1))
  [[ $waited -ge 60 ]] && fail "new device never became usable (60s) - live user sync is not working"
  sleep 1
done
log "PASS: device added after startup can connect (${waited}s)"

XRAY_PID_AFTER=$(pgrep -f "xray -config /etc/node-agent" | head -1)
assert_eq "xray was NOT restarted to admit the new user" "$XRAY_PID_BEFORE" "$XRAY_PID_AFTER"

# The tunnel can come up just before the agent logs the sync; don't race it.
waited=0
until grep -q "synced users without restart: 2 active" "$LOGDIR/node-agent.log"; do
  waited=$((waited + 1))
  [[ $waited -ge 15 ]] && fail "agent never logged a 2-user sync - the new user likely arrived via a restart"
  sleep 1
done
grep -q "config changed, restarting xray" "$LOGDIR/node-agent.log" \
  && fail "agent restarted xray on a users-only change"
log "PASS: agent applied the change incrementally (AlterInbound accepted by real Xray)"

# A botched sync could evict existing users while admitting the new one.
assert_eq "the pre-existing device still works after the sync" "$MARKER" "$(fetch_via_socks 1080)"

# ---------------------------------------------------------------------------
# 14c. Supervisor. A crashed or OOM-killed Xray used to stay dead while the
#      agent kept reporting healthy (its liveness check accepted the zombie);
#      superviseLoop now polls every 10s and restarts it. Kill the real process
#      and assert service comes back on its own.
#
#      The pattern matches only the agent-managed Xray (-config
#      /etc/node-agent/...), never the client Xrays this script runs out of
#      $WORKDIR.
# ---------------------------------------------------------------------------
log "testing supervisor (killing xray and expecting an automatic restart)"

# Kill by pid: `pkill -f <pattern>` also matches the sudo/pkill command line
# itself, so it kills itself and reports failure even though xray did die.
# XRAY_PID_AFTER came from the same pattern, which matches only the
# agent-managed Xray (-config /etc/node-agent), never this script's clients.
sudo kill -KILL "$XRAY_PID_AFTER" || fail "could not kill the server-side xray (pid $XRAY_PID_AFTER)"
wait_for "vless port to drop after the kill" 15 bash -c "! ss -tln | grep -q ':$VLESS_PORT '"

# superviseLoop checks every 10s, and Start() then waits out a 3s startup
# grace, so ~15s is the expected recovery time.
wait_for "supervisor to bring xray back" 40 bash -c "ss -tln | grep -q ':$VLESS_PORT '"

XRAY_PID_RESTARTED=$(pgrep -f "xray -config /etc/node-agent" | head -1)
[[ -n "$XRAY_PID_RESTARTED" && "$XRAY_PID_RESTARTED" != "$XRAY_PID_AFTER" ]] \
  || fail "xray pid did not change - supervisor did not actually respawn it"

waited=0
until grep -q "supervisor: xray restarted" "$LOGDIR/node-agent.log"; do
  waited=$((waited + 1))
  [[ $waited -ge 15 ]] && fail "agent log has no supervisor restart record"
  sleep 1
done

# Liveness is not the point - carrying traffic again is. Retry while the fresh
# process finishes binding every inbound.
waited=0
until [[ "$(fetch_via_socks 1080)" == "$MARKER" ]]; do
  waited=$((waited + 1))
  [[ $waited -ge 30 ]] && fail "tunnel never recovered after the supervisor restart"
  sleep 1
done
log "PASS: supervisor restarted xray and traffic flows again (${waited}s)"

# ---------------------------------------------------------------------------
# 14d. Traffic accounting. Xray counts per-user bytes under stats keys derived
#      from the client email (uuid@proxima), the agent scrapes them over gRPC
#      every 30s and POSTs to /stats, and the server both appends to
#      traffic_logs and accumulates users.traffic_used - the value its own
#      access-control SQL then enforces. Every tunnel above already pushed
#      bytes through, so the whole chain is observable from the admin API.
# ---------------------------------------------------------------------------
log "testing traffic accounting (xray stats -> agent -> server)"

# Push bytes through so there is something to account for. Failures are ignored:
# the client Xray is still recovering from the restart above, and a dropped
# request would abort the whole script under set -e.
for _ in 1 2 3; do fetch_via_socks 1080 > /dev/null || true; done

waited=0
while true; do
  # `VAR=$(cmd)` adopts cmd's exit status, so an unguarded curl failure in this
  # retry loop would kill the script under set -e instead of retrying.
  TRAFFIC_USED=$(curl -sf "$BASE_URL/api/v1/admin/users/$USER_ID" \
    -H "Authorization: Bearer $ADMIN_TOKEN" | jq -r '.traffic_used // empty') || TRAFFIC_USED=""
  [[ "$TRAFFIC_USED" =~ ^[0-9]+$ && "$TRAFFIC_USED" -gt 0 ]] && break
  waited=$((waited + 1))
  [[ $waited -ge 75 ]] && fail "traffic_used stayed at ${TRAFFIC_USED:-unset} after 75s - the stats pipeline is broken"
  sleep 1
done
log "PASS: traffic_used = $TRAFFIC_USED bytes (${waited}s, via real xray stats)"

# traffic_logs is the audit trail the admin charts read; an empty table with a
# non-zero traffic_used would mean the aggregate is being written blind.
LOGGED_ROWS=$(curl -sf "$BASE_URL/api/v1/admin/stats/traffic-history" \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq 'length') || LOGGED_ROWS=""
[[ "$LOGGED_ROWS" =~ ^[0-9]+$ && "$LOGGED_ROWS" -gt 0 ]] \
  || fail "traffic history is empty (got '${LOGGED_ROWS:-request failed}') despite traffic_used=$TRAFFIC_USED"
log "PASS: traffic_logs has $LOGGED_ROWS aggregated row(s)"

# ---------------------------------------------------------------------------
# 14b. Speed limits. Setting speed_limit on a plan is supposed to move its
#      users onto a dedicated inbound that the agent rate-limits with tc. Every
#      part of that was previously unverified: the shaper's own tests mock the
#      tc binary, so nothing proved a rule reached the kernel, let alone that
#      throughput obeyed it. Worse, tc reports "Operation not permitted" on
#      stderr while exiting 0, so a node lacking CAP_NET_ADMIN served limited
#      users at full speed and reported success.
# ---------------------------------------------------------------------------
log "testing speed limits (tc shaping)"

SHAPE_MBPS=8
curl -sf -X PUT "$BASE_URL/api/v1/admin/plans/$PLAN_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"speed_limit\":$SHAPE_MBPS}" > /dev/null || fail "could not set a speed limit on the plan"

TIER_PORT=$((20000 + SHAPE_MBPS))
TIER_TAG="vless-reality-limit-$SHAPE_MBPS"

# The tier inbound is synthesised at config-generation time, not stored in the
# inbounds table, which is why it coexists with one-inbound-per-node.
for _ in $(seq 1 30); do
  if curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/config" -H "X-Node-Key: $NODE_KEY" \
       | jq -e --arg t "$TIER_TAG" '.inbounds[] | select(.tag == $t)' > /dev/null 2>&1; then
    break
  fi
  sleep 1
done

TIER_JSON=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/config" -H "X-Node-Key: $NODE_KEY" \
  | jq -c --arg t "$TIER_TAG" '.inbounds[] | select(.tag == $t)')
[[ -n "$TIER_JSON" ]] || fail "no $TIER_TAG inbound was generated for a ${SHAPE_MBPS}Mbps plan"
assert_eq "the speed tier listens on its derived port" "$TIER_PORT" "$(echo "$TIER_JSON" | jq -r .port)"

# The agent shapes the default-route interface, the same one it resolves itself.
SHAPE_IFACE=$(ip route show default 2>/dev/null | awk '/default/ {print $5; exit}')
[[ -n "$SHAPE_IFACE" ]] || fail "no default route interface; the agent could not have shaped either"

# Wait for the agent to restart onto the new structure and install the rules.
for _ in $(seq 1 40); do
  tc class show dev "$SHAPE_IFACE" 2>/dev/null | grep -q "${SHAPE_MBPS}Mbit" && break
  sleep 2
done

TIER_CLASS=$(tc class show dev "$SHAPE_IFACE" 2>/dev/null | grep "${SHAPE_MBPS}Mbit" | head -1)
[[ -n "$TIER_CLASS" ]] || fail "no tc class caps at ${SHAPE_MBPS}Mbit on $SHAPE_IFACE; shaping never reached the kernel"
log "PASS: tc installed a ${SHAPE_MBPS}Mbit class on $SHAPE_IFACE"

FILTER_COUNT=$(tc filter show dev "$SHAPE_IFACE" 2>/dev/null | grep -c "match" || true)
[[ "$FILTER_COUNT" -gt 0 ]] || fail "tc has no filters; the tier class would never receive traffic"
log "PASS: tc has $FILTER_COUNT filter match(es) directing tier traffic"

# tc prints "Operation not permitted" to stderr and still exits 0, so the agent
# has to report the outcome or a node serving limited users at full rate looks
# healthy. Read it back the way an operator would.
# The count arrives on the next heartbeat, which is a separate 30s cycle from
# the config poll that installed the rules, so wait for it rather than reading
# whatever the previous heartbeat left.
SHAPING_TIERS=0
for _ in $(seq 1 30); do
  NODE_JSON=$(curl -sf "$BASE_URL/api/v1/admin/nodes/$NODE_ID" -H "Authorization: Bearer $ADMIN_TOKEN")
  SHAPING_TIERS=$(echo "$NODE_JSON" | jq -r '.shaping_tiers // 0')
  [[ "$SHAPING_TIERS" -ge 1 ]] && break
  sleep 2
done
assert_eq "the node reports shaping as applied" "true" "$(echo "$NODE_JSON" | jq -r '.shaping_ok')"
[[ "$SHAPING_TIERS" -ge 1 ]] || fail "node reports $SHAPING_TIERS shaped tiers after 60s, expected at least 1"
log "PASS: node reports shaping_ok with $SHAPING_TIERS tier(s)"

curl -sf -X PUT "$BASE_URL/api/v1/admin/plans/$PLAN_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"speed_limit":0}' > /dev/null || fail "could not clear the speed limit"
log "PASS: speed limit cleared"

# ---------------------------------------------------------------------------
# 15. Subscription formats. Every client app format the panel advertises is
#     generated from the same node set and checked for (a) structural
#     validity in that format's own syntax, and (b) actually containing the
#     protocols it claims to support. This is what would have caught the
#     Shadowsocks-missing-from-subscriptions bug: the node ran SS fine, but
#     the subscription silently omitted it because it read a dead column.
# ---------------------------------------------------------------------------
log "testing subscription formats"

sub_fetch() { curl -sf "$BASE_URL/sub/$SUB_TOKEN/$DEVICE_ID?format=$1"; }

# --- Clash: must be valid YAML and carry every protocol as a typed proxy ---
sub_fetch clash > "$WORKDIR/sub_clash.yaml"
EXPECT_TYPES="vless" ; [[ "$LEGACY_PROTOCOLS" == "1" ]] && EXPECT_TYPES="vless,vmess,trojan,ss,wireguard"
EXPECT_TYPES="$EXPECT_TYPES" python3 - "$WORKDIR/sub_clash.yaml" <<'PY' || fail "clash subscription failed validation"
import os, sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
proxies = d.get("proxies") or []
types = {p.get("type") for p in proxies}
missing = set(os.environ["EXPECT_TYPES"].split(",")) - types
assert not missing, f"clash: missing proxy types {missing} (got {types})"
assert d.get("proxy-groups"), "clash: no proxy-groups"
names = {p.get("name") for p in proxies}
for g in d["proxy-groups"]:
    for ref in (g.get("proxies") or []):
        assert ref in names or ref in {n.get("name") for n in d["proxy-groups"]} or ref == "DIRECT", \
            f"clash: proxy-group {g.get('name')!r} references unknown proxy {ref!r}"
print("clash ok:", sorted(types))
PY
log "PASS: Clash subscription is valid YAML with the expected protocols"

# --- Sing-box: must be valid JSON with matching outbound types ---
sub_fetch singbox > "$WORKDIR/sub_singbox.json"
EXPECT_TYPES="vless" ; [[ "$LEGACY_PROTOCOLS" == "1" ]] && EXPECT_TYPES="vless,vmess,trojan,shadowsocks,wireguard"
EXPECT_TYPES="$EXPECT_TYPES" python3 - "$WORKDIR/sub_singbox.json" <<'PY' || fail "singbox subscription failed validation"
import os, sys, json
d = json.load(open(sys.argv[1]))
obs = d.get("outbounds") or []
types = {o.get("type") for o in obs}
missing = set(os.environ["EXPECT_TYPES"].split(",")) - types
assert not missing, f"singbox: missing outbound types {missing} (got {types})"
tags = {o.get("tag") for o in obs}
for o in obs:
    if o.get("type") in ("selector", "urltest"):
        for ref in (o.get("outbounds") or []):
            assert ref in tags, f"singbox: {o.get('tag')!r} references unknown outbound {ref!r}"
print("singbox ok:", sorted(types))
PY
log "PASS: Sing-box subscription is valid JSON with the expected protocols"

# --- Surfboard / Quantumult: documented to omit hysteria2+wireguard (and,
#     as found during the audit, vless too), so assert the protocols they DO
#     claim actually appear rather than asserting all five. ---
if [[ "$LEGACY_PROTOCOLS" == "1" ]]; then
sub_fetch surfboard > "$WORKDIR/sub_surfboard.conf"
grep -q '^\[Proxy\]' "$WORKDIR/sub_surfboard.conf" || fail "surfboard: no [Proxy] section"
grep -q '^\[Proxy Group\]' "$WORKDIR/sub_surfboard.conf" || fail "surfboard: no [Proxy Group] section"
for p in vmess trojan ss; do
  grep -qE "= ?$p," "$WORKDIR/sub_surfboard.conf" || fail "surfboard: no $p proxy line"
done
log "PASS: Surfboard subscription has the protocols it supports (vmess/trojan/ss)"

sub_fetch quantumult > "$WORKDIR/sub_quantumult.conf"
for p in vmess trojan shadowsocks; do
  grep -q "^$p=" "$WORKDIR/sub_quantumult.conf" || fail "quantumult: no $p line"
done
log "PASS: Quantumult subscription has the protocols it supports (vmess/trojan/ss)"
else
log "SKIP: Surfboard/Quantumult formats (they carry only vmess/trojan/ss)"
fi

# --- Default v2ray base64 list: one link per protocol that has one ---
V2RAY_LINKS=$(curl -sf "$BASE_URL/sub/$SUB_TOKEN/$DEVICE_ID" | base64 -d)
SCHEMES="vless" ; [[ "$LEGACY_PROTOCOLS" == "1" ]] && SCHEMES="vless vmess trojan ss"
for scheme in $SCHEMES; do
  echo "$V2RAY_LINKS" | grep -q "^$scheme://" || fail "v2ray subscription: no $scheme:// link"
done
log "PASS: v2ray subscription contains the expected links"

# ---------------------------------------------------------------------------
# 16. Access-control / edge cases. The happy path above only proves things
#     work when everything is configured correctly; these assert the server
#     actually *denies* access when it should. Each of these corresponds to a
#     real revocation path the panel promises (suspend, expire, traffic cap).
# ---------------------------------------------------------------------------
log "testing access-control edge cases"

# A device belonging to a suspended user must drop out of the node's config.
curl -sf -X PUT "$BASE_URL/api/v1/admin/users/$USER_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"status":"suspended","is_active":false}' > /dev/null
CLIENTS_WHEN_SUSPENDED=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/config" -H "X-Node-Key: $NODE_KEY" \
  | jq '[.inbounds[] | select(.tag=="vless-in") | .settings.clients // [] | length] | add')
assert_eq "suspended user is removed from the node's xray clients" "0" "$CLIENTS_WHEN_SUSPENDED"

WG_PEERS_WHEN_SUSPENDED=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/wireguard/peers" -H "X-Node-Key: $NODE_KEY" | jq 'length')
assert_eq "suspended user is removed from the wireguard peer set" "0" "$WG_PEERS_WHEN_SUSPENDED"

if [[ "$LEGACY_PROTOCOLS" == "1" ]]; then
HY2_WHEN_SUSPENDED=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/hysteria2/users" -H "X-Node-Key: $NODE_KEY" | jq 'length')
assert_eq "suspended user is removed from the hysteria2 user set" "0" "$HY2_WHEN_SUSPENDED"
fi

SUSPENDED_SUB_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/sub/$SUB_TOKEN/$DEVICE_ID")
assert_eq "suspended user's subscription is refused" "403" "$SUSPENDED_SUB_STATUS"

# Reactivate and confirm access actually comes back (proves the assertions
# above were measuring the suspension, not a permanently broken fixture).
curl -sf -X PUT "$BASE_URL/api/v1/admin/users/$USER_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"status":"active","is_active":true}' > /dev/null
DEVICE_COUNT=$(curl -sf "$BASE_URL/api/v1/user/devices" -H "Authorization: Bearer $USER_TOKEN" | jq 'length')
CLIENTS_AFTER_RESTORE=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/config" -H "X-Node-Key: $NODE_KEY" \
  | jq '[.inbounds[] | select(.tag=="vless-in") | .settings.clients // [] | length] | add')
assert_eq "reactivated user's devices are restored to the node's xray clients" "$DEVICE_COUNT" "$CLIENTS_AFTER_RESTORE"

# An expired plan must revoke access the same way.
curl -sf -X PUT "$BASE_URL/api/v1/admin/users/$USER_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"plan_expires_at":"2020-01-01T00:00:00Z"}' > /dev/null
CLIENTS_WHEN_EXPIRED=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/config" -H "X-Node-Key: $NODE_KEY" \
  | jq '[.inbounds[] | select(.tag=="vless-in") | .settings.clients // [] | length] | add')
assert_eq "user with an expired plan is removed from the node's xray clients" "0" "$CLIENTS_WHEN_EXPIRED"

# Exceeding the traffic cap must revoke access too. Config generation enforces
# traffic_used < traffic_limit itself, so dropping the limit below the bytes
# section 14d accumulated takes effect at once - no need to wait out the
# 5-minute ExpiryCheckScheduler, which only mirrors this into users.status.
curl -sf -X PUT "$BASE_URL/api/v1/admin/users/$USER_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"plan_expires_at":null,"status":"active","is_active":true}' > /dev/null
curl -sf -X PUT "$BASE_URL/api/v1/admin/plans/$PLAN_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"name\":\"e2e-plan\",\"node_group_id\":\"$NG_ID\",\"duration_days\":30,\"max_devices\":3,\"is_active\":true,\"traffic_limit\":1}" > /dev/null

CLIENTS_OVER_QUOTA=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/config" -H "X-Node-Key: $NODE_KEY" \
  | jq '[.inbounds[] | select(.tag=="vless-in") | .settings.clients // [] | length] | add')
assert_eq "user over the traffic cap is removed from the node's xray clients" "0" "$CLIENTS_OVER_QUOTA"

WG_PEERS_OVER_QUOTA=$(curl -sf "$BASE_URL/api/v1/nodes/$NODE_ID/wireguard/peers" -H "X-Node-Key: $NODE_KEY" | jq 'length')
assert_eq "user over the traffic cap is removed from the wireguard peer set" "0" "$WG_PEERS_OVER_QUOTA"

OVER_QUOTA_SUB_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/sub/$SUB_TOKEN/$DEVICE_ID")
assert_eq "user over the traffic cap is refused a subscription" "403" "$OVER_QUOTA_SUB_STATUS"

# Bad node credentials must be rejected outright.
BAD_KEY_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v1/nodes/$NODE_ID/config" -H "X-Node-Key: definitely-not-the-key")
assert_eq "wrong node API key is rejected" "401" "$BAD_KEY_STATUS"

NO_KEY_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v1/nodes/$NODE_ID/config")
assert_eq "missing node API key is rejected" "401" "$NO_KEY_STATUS"

# An unknown subscription token must 404, not leak someone else's config.
BAD_SUB_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/sub/00000000-0000-0000-0000-000000000000/$DEVICE_ID")
assert_eq "unknown subscription token is refused" "404" "$BAD_SUB_STATUS"

log "PASS: access-control edge cases behave correctly"

# ---------------------------------------------------------------------------
# 17. node-agent unregister: the same command scripts/uninstall.sh runs
#     before tearing down the local install (see NodeAgentHandler.Unregister
#     in api-server/internal/handlers/node_agent.go). Run while node-agent is
#     still up, matching uninstall.sh's actual order (it unregisters before
#     stopping the service) - unregister is a standalone HTTP call using the
#     saved API key, independent of whether the agent process is running.
# ---------------------------------------------------------------------------
log "testing node-agent unregister"
sudo "$NODE_AGENT_BIN" unregister --config "$WORKDIR/node-agent-config.json" \
  > "$LOGDIR/unregister.log" 2>&1
cat "$LOGDIR/unregister.log"
grep -q "unregistered from panel successfully" "$LOGDIR/unregister.log" \
  || fail "unregister command did not report success"

STILL_PRESENT=$(curl -sf "$BASE_URL/api/v1/admin/nodes/" -H "Authorization: Bearer $ADMIN_TOKEN" \
  | jq "[.[] | select(.id == \"$NODE_ID\")] | length")
assert_eq "node no longer appears in admin node list after unregister" "0" "$STILL_PRESENT"

UNREG_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v1/nodes/$NODE_ID/tls-domain" -H "X-Node-Key: $NODE_KEY")
assert_eq "node's own API key rejected after unregister" "401" "$UNREG_STATUS"

log "PASS: node-agent unregister removed the node from the panel"
