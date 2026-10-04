#!/usr/bin/env bash
set -euo pipefail

api=${E2E_API_URL}

json_post() {
  local path=$1 token=$2 body=$3
  curl -fsS -X POST "${api}${path}" \
    ${token:+-H "Authorization: Bearer ${token}"} \
    -H 'Content-Type: application/json' -d "$body"
}

json_put() {
  local path=$1 token=$2 body=$3
  curl -fsS -X PUT "${api}${path}" \
    -H "Authorization: Bearer ${token}" \
    -H 'Content-Type: application/json' -d "$body"
}

require_id() {
  jq -er "$1 | select(type == \"string\" and length > 0)"
}

login=$(json_post /api/v1/admin/auth/login '' \
  "$(jq -nc --arg email "$ADMIN_EMAIL" --arg password "$ADMIN_PASSWORD" '{email:$email,password:$password}')")
admin_token=$(require_id .token <<<"$login")

pending=$(json_post /api/v1/admin/nodes/token "$admin_token" \
  '{"role":"exit","name":"direct-e2e-exit","port":8443}')
registered=$(json_post /api/v1/nodes/register '' \
  "$(jq -nc --arg token "$(jq -r .token <<<"$pending")" --arg ip "$EXIT_IP" \
    '{reg_token:$token,ip:$ip,port:8443,xray_version:"v26.7.28",name:"direct-e2e-exit",country:"ZZ",region:"direct-e2e"}')")
exit_id=$(require_id .node_id <<<"$registered")
exit_key=$(require_id .api_key <<<"$registered")
echo "PASS: registered one exit-only node ${exit_id}"

group=$(json_post /api/v1/admin/node-groups/ "$admin_token" '{"name":"direct-e2e-access"}')
group_id=$(require_id .id <<<"$group")
json_put "/api/v1/admin/node-groups/${group_id}/nodes" "$admin_token" \
  "$(jq -nc --arg id "$exit_id" '{node_ids:[$id]}')" >/dev/null

direct_chain=$(curl -fsS "${api}/api/v1/admin/node-chains" -H "Authorization: Bearer ${admin_token}" |
  jq -er --arg id "$exit_id" '.[] | select(.exit_node_id == $id and .entry_node_id == null and .relay_pool_id == null)')
[[ "$(jq -r '.group_ids | length' <<<"$direct_chain")" == 1 ]] || {
  echo 'FAIL: direct node group did not publish its direct chain' >&2
  exit 1
}
echo 'PASS: exit direct chain published to its subscription group'

inbound=$(json_post "/api/v1/admin/nodes/${exit_id}/inbounds" "$admin_token" \
  '{"protocol":"vless_reality","port":8443,"tag":"direct-vless","settings":{"dest":"127.0.0.1:9443","server_names":["www.cloudflare.com"]}}')
inbound_id=$(require_id .id <<<"$inbound")
echo "PASS: created VLESS Reality inbound ${inbound_id}"

plan=$(json_post /api/v1/admin/plans/ "$admin_token" \
  "$(jq -nc --arg group "$group_id" '{name:"direct-e2e-plan",node_group_id:$group,duration_days:30,max_devices:1,prices:[],features:[]}')")
plan_id=$(require_id .id <<<"$plan")
user=$(json_post /api/v1/admin/users/ "$admin_token" \
  "$(jq -nc --arg plan "$plan_id" '{email:"direct-e2e-user@example.test",password:"DirectE2EUserPassword123!",name:"direct e2e user",plan_id:$plan,is_active:true}')")
require_id .id <<<"$user" >/dev/null
user_login=$(json_post /api/v1/auth/login '' \
  '{"email":"direct-e2e-user@example.test","password":"DirectE2EUserPassword123!"}')
user_token=$(require_id .token <<<"$user_login")
device=$(json_post /api/v1/user/devices "$user_token" '{"name":"direct-e2e-device"}')
device_id=$(require_id .id <<<"$device")
device_uuid=$(require_id .xray_uuid <<<"$device")
subscription_token=$(require_id .sub_token <<<"$(json_post /api/v1/user/sub-token/regenerate "$user_token" '{}')")

subscription=$(curl -fsS "${api}/sub/${subscription_token}/${device_id}" | base64 -d)
link=$(sed -n '/^vless:\/\//{p;q;}' <<<"$subscription")
[[ "$link" == vless://"$device_uuid"@"$EXIT_IP":8443\?* ]] || {
  echo 'FAIL: subscription did not advertise the direct exit endpoint' >&2
  exit 1
}
server_config=$(curl -fsS "${api}/api/v1/nodes/${exit_id}/config" -H "X-Node-Key: ${exit_key}")
private_key=$(jq -er '.inbounds[] | select(.protocol == "vless") | .streamSettings.realitySettings.privateKey' <<<"$server_config")
server_public=$(xray x25519 -i "$private_key" | awk '/^Password \(PublicKey\):/ {print $3}')
server_short=$(jq -er '.inbounds[] | select(.protocol == "vless") | .streamSettings.realitySettings.shortIds[0]' <<<"$server_config")
[[ "$link" == *"pbk=${server_public}"* && "$link" == *"sid=${server_short}"* ]] || {
  echo 'FAIL: direct subscription Reality credentials differ from exit config' >&2
  exit 1
}
printf '%s\n' "$link" > /client-state/vless-link.txt
jq -nc --arg id "$exit_id" --arg key "$exit_key" --arg url "$api" \
  '{node_id:$id,api_key:$key,server_url:$url}' > /exit-state/exit-agent.json
echo 'PASS: subscription has the direct exit endpoint and generated Reality credentials'
