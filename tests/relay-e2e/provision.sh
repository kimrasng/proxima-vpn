#!/usr/bin/env bash
set -euo pipefail

api=${E2E_API_URL}
state=/state/e2e.env
host=entry.relay-e2e.test
sni=decoy.relay-e2e.test

post() {
  local path=$1 token=$2 body=$3
  curl -fsS -X POST "${api}${path}" ${token:+-H "Authorization: Bearer ${token}"} \
    -H 'Content-Type: application/json' -d "$body"
}
put() {
  curl -fsS -X PUT "${api}$1" -H "Authorization: Bearer $2" \
    -H 'Content-Type: application/json' -d "$3"
}
id() { jq -er "$1 | select(type == \"string\" and length > 0)"; }
node() {
  local role=$1 name=$2 ip=$3 port=$4 token registration
  token=$(post /api/v1/admin/nodes/token "$ADMIN_TOKEN" \
    "$(jq -nc --arg role "$role" --arg name "$name" --argjson port "$port" '{role:$role,name:$name,port:$port}')" | id .token)
  registration=$(post /api/v1/nodes/register '' \
    "$(jq -nc --arg token "$token" --arg ip "$ip" --arg name "$name" --argjson port "$port" \
      '{reg_token:$token,ip:$ip,port:$port,xray_version:"v26.7.28",name:$name,country:"ZZ",region:"relay-e2e"}')")
  printf '%s\t%s\n' "$(id .node_id <<<"$registration")" "$(id .api_key <<<"$registration")"
}
group() {
  local name=$1 member=$2 response
  response=$(post /api/v1/admin/node-groups/ "$ADMIN_TOKEN" "$(jq -nc --arg name "$name" '{name:$name}')")
  local group_id
  group_id=$(id .id <<<"$response")
  put "/api/v1/admin/node-groups/${group_id}/nodes" "$ADMIN_TOKEN" \
    "$(jq -nc --arg id "$member" '{node_ids:[$id]}')" >/dev/null
  printf '%s' "$group_id"
}
user_profile() {
  local label=$1 exit_id=$2 group_id=$3 port=$4 key=$5 chain inbound plan user login device sub
  chain=$(post /api/v1/admin/node-chains/ "$ADMIN_TOKEN" \
    "$(jq -nc --arg entry "$ENTRY_ID" --arg host "$host" --arg exit "$exit_id" \
      --arg name "chain-$label" --argjson port "$port" \
      '{name:$name,entry_node_id:$entry,entry_host:$host,entry_port:$port,exit_node_id:$exit,exit_port:8443,transport:"tcp"}')")
  [[ $(jq -er .entry_port <<<"$chain") == "$port" && $(jq -er .entry_host <<<"$chain") == "$host" ]] || exit 1
  local chain_id
  chain_id=$(id .id <<<"$chain")
  put "/api/v1/admin/node-chains/${chain_id}/groups" "$ADMIN_TOKEN" \
    "$(jq -nc --arg id "$group_id" '{group_ids:[$id]}')" >/dev/null
  inbound=$(post "/api/v1/admin/nodes/${exit_id}/inbounds" "$ADMIN_TOKEN" \
    "$(jq -nc --arg sni "$sni" '{protocol:"vless_reality",port:8443,tag:"vless-in",settings:{dest:"127.0.0.1:9443",server_names:[$sni]}}')")
  id .id <<<"$inbound" >/dev/null
  plan=$(post /api/v1/admin/plans/ "$ADMIN_TOKEN" \
    "$(jq -nc --arg group "$group_id" --arg name "plan-$label" \
      '{name:$name,node_group_id:$group,duration_days:30,max_devices:1,prices:[],features:[]}')")
  local plan_id email password user_id user_token device_id
  plan_id=$(id .id <<<"$plan")
  email="user-${label}@relay-e2e.test"
  password="RelayE2E-${label}-$(openssl rand -hex 12)!"
  user=$(post /api/v1/admin/users/ "$ADMIN_TOKEN" \
    "$(jq -nc --arg plan "$plan_id" --arg email "$email" --arg password "$password" \
      '{email:$email,password:$password,name:"relay e2e user",plan_id:$plan,is_active:true}')")
  user_id=$(id .id <<<"$user")
  login=$(post /api/v1/auth/login '' "$(jq -nc --arg email "$email" --arg password "$password" '{email:$email,password:$password}')")
  user_token=$(id .token <<<"$login")
  device=$(post /api/v1/user/devices "$user_token" "$(jq -nc --arg name "device-$label" '{name:$name}')")
  device_id=$(id .id <<<"$device")
  sub=$(post /api/v1/user/sub-token/regenerate "$user_token" '{}')
  printf 'CHAIN_%s=%q\nEXIT_%s=%q\nEXIT_KEY_%s=%q\nDEVICE_%s=%q\nUUID_%s=%q\nSUB_%s=%q\n' \
    "$label" "$chain_id" "$label" "$exit_id" "$label" "$key" "$label" "$device_id" \
    "$label" "$(id .xray_uuid <<<"$device")" "$label" "$(id .sub_token <<<"$sub")" >>"$state"
}
provision() {
  local login entry a b
  login=$(post /api/v1/admin/auth/login '' \
    "$(jq -nc --arg email "$ADMIN_EMAIL" --arg password "$ADMIN_PASSWORD" '{email:$email,password:$password}')")
  ADMIN_TOKEN=$(id .token <<<"$login")
  entry=$(node relay e2e-entry "$RELAY_BACK_IP" 443)
  IFS=$'\t' read -r ENTRY_ID ENTRY_KEY <<<"$entry"
  a=$(node exit e2e-exit-a "$EXIT_A_BACK_IP" 8443)
  IFS=$'\t' read -r EXIT_A EXIT_KEY_A <<<"$a"
  b=$(node exit e2e-exit-b "$EXIT_B_BACK_IP" 8443)
  IFS=$'\t' read -r EXIT_B EXIT_KEY_B <<<"$b"
  put "/api/v1/admin/nodes/${EXIT_A}" "$ADMIN_TOKEN" '{"publish_direct":false}' >/dev/null
  put "/api/v1/admin/nodes/${EXIT_B}" "$ADMIN_TOKEN" '{"publish_direct":false}' >/dev/null
  # Stored local fixture, not a claim that DNS was published by Cloudflare.
  psql -h postgres -U relay_e2e -d relay_e2e -v ON_ERROR_STOP=1 -v entry="$ENTRY_ID" \
    -v ip="$RELAY_BACK_IP" -v host="$host" -q >/dev/null <<'SQL'
INSERT INTO managed_entry_dns(owner_node_id,node_id,hostname,desired_ipv4,dns_status)
VALUES (:'entry',:'entry',:'host',:'ip','ready');
SQL
  : >"$state"
  printf 'ADMIN_TOKEN=%q\nENTRY_ID=%q\nEXIT_A=%q\nEXIT_B=%q\n' \
    "$ADMIN_TOKEN" "$ENTRY_ID" "$EXIT_A" "$EXIT_B" >>"$state"
  local group_a group_b
  group_a=$(group access-a "$EXIT_A")
  group_b=$(group access-b "$EXIT_B")
  user_profile A "$EXIT_A" "$group_a" 24443 "$EXIT_KEY_A"
  user_profile B "$EXIT_B" "$group_b" 24444 "$EXIT_KEY_B"
  jq -nc --arg id "$ENTRY_ID" --arg key "$ENTRY_KEY" --arg url "$api" \
    '{node_id:$id,api_key:$key,server_url:$url}' >/relay-state/relay-agent.json
  for label in A B; do
    local node_var="EXIT_${label}" key_var="EXIT_KEY_${label}" node_id key
    node_id=${!node_var}
    key=${!key_var}
    jq -nc --arg id "$node_id" --arg key "$key" --arg url "$api" \
      '{node_id:$id,api_key:$key,server_url:$url}' >"/exit-${label,,}-state/exit-${label,,}-agent.json"
  done
  echo 'Provisioned one stored managed Entry and two explicit API chains'
}
subscription() {
  source "$state"
  local label exit_id key device uuid token config link decoded public private short server_uuid server_sni
  for label in A B; do
    local exit_var="EXIT_${label}" key_var="EXIT_KEY_${label}" device_var="DEVICE_${label}" uuid_var="UUID_${label}" sub_var="SUB_${label}"
    exit_id=${!exit_var}; key=${!key_var}; device=${!device_var}; uuid=${!uuid_var}; token=${!sub_var}
    for attempt in {1..60}; do
      decoded=$(curl -fsS "${api}/sub/${token}/${device}" | base64 -d)
      link=$(sed -n '/^vless:\/\//{p;q;}' <<<"$decoded")
      [[ -n "$link" ]] && break
      ((attempt < 60)) || { echo 'subscription not published after agent acknowledgement' >&2; exit 1; }
      sleep 1
    done
    config=$(curl -fsS "${api}/api/v1/nodes/${exit_id}/config" -H "X-Node-Key: ${key}")
    private=$(jq -er '.inbounds[] | select(.protocol == "vless") | .streamSettings.realitySettings.privateKey' <<<"$config")
    public=$(xray x25519 -i "$private" | awk '/^Password \(PublicKey\):/ {print $3}')
    short=$(jq -er '.inbounds[] | select(.protocol == "vless") | .streamSettings.realitySettings.shortIds[0]' <<<"$config")
    server_uuid=$(jq -er '.inbounds[] | select(.protocol == "vless") | .settings.clients[0].id' <<<"$config")
    server_sni=$(jq -er '.inbounds[] | select(.protocol == "vless") | .streamSettings.realitySettings.serverNames[0]' <<<"$config")
    local port=24443
    [[ "$label" == B ]] && port=24444
    [[ "$server_uuid" == "$uuid" && "$server_sni" == "$sni" && "$link" == "vless://${uuid}@${host}:${port}?"* &&
       "$link" == *"pbk=${public}"* && "$link" == *"sid=${short}"* && "$link" == *"sni=${sni}"* ]] || {
      echo 'subscription endpoint or credentials differ from generated Exit config' >&2; exit 1;
    }
    printf '%s\n' "$link" >"/client-state/link-${label}.txt"
    echo "Profile ${label}: hostname, port, UUID, public key, short ID, SNI match acknowledged Exit config"
  done
  [[ "$UUID_A" != "$UUID_B" ]] || exit 1
}
legacy() {
  source "$state"
  local pool
  pool=$(post /api/v1/admin/node-groups/ "$ADMIN_TOKEN" '{"name":"legacy-relay-pool"}')
  POOL_ID=$(id .id <<<"$pool")
  put "/api/v1/admin/node-groups/${POOL_ID}/nodes" "$ADMIN_TOKEN" \
    "$(jq -nc --arg id "$ENTRY_ID" '{node_ids:[$id]}')" >/dev/null
  printf 'POOL_ID=%q\n' "$POOL_ID" >>"$state"
  psql -h postgres -U relay_e2e -d relay_e2e -v ON_ERROR_STOP=1 -v pool="$POOL_ID" -v exit="$EXIT_A" -v host="$host" -q >/dev/null <<'SQL'
INSERT INTO node_chains(name,relay_pool_id,entry_host,entry_port,exit_node_id,exit_port,transport,mode,enabled)
VALUES ('DB-seeded legacy compatibility',:'pool',:'host',24445,:'exit',8443,'tcp','l4_dnat',true);
SQL
  echo 'DB-seeded legacy compatibility chain (API rejects new pool chains)'
}
fault() {
  source "$state"
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT \
    "${api}/api/v1/admin/node-groups/${POOL_ID}/nodes" -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H 'Content-Type: application/json' -d '{"node_ids":[]}')
  [[ "$code" == 409 ]] || { echo "expected API 409, got $code" >&2; exit 1; }
  psql -h postgres -U relay_e2e -d relay_e2e -v ON_ERROR_STOP=1 -v pool="$POOL_ID" -q >/dev/null <<'SQL'
DELETE FROM node_group_nodes WHERE node_group_id=:'pool';
SQL
  echo 'API 409; disposable DB legacy membership emptied'
}
restore() {
  source "$state"
  psql -h postgres -U relay_e2e -d relay_e2e -v ON_ERROR_STOP=1 -v pool="$POOL_ID" -v entry="$ENTRY_ID" -q >/dev/null <<'SQL'
INSERT INTO node_group_nodes(node_group_id,node_id) VALUES (:'pool',:'entry');
SQL
}
case "$E2E_ACTION" in
  provision) provision ;; subscription) subscription ;; legacy) legacy ;;
  fault) fault ;; restore) restore ;;
  *) exit 1 ;;
esac
