#!/usr/bin/env bash
set -euo pipefail

# Stage 2: runs after the Exit agent is up. The subscription link is published
# only once the running agent has acknowledged its config, so poll for it.
api=${E2E_API_URL}
state=/client-state/direct-e2e-state.json
exit_id=$(jq -er .exit_id "$state")
exit_key=$(jq -er .exit_key "$state")
device_id=$(jq -er .device_id "$state")
device_uuid=$(jq -er .device_uuid "$state")
subscription_token=$(jq -er .sub_token "$state")

link=
for attempt in {1..60}; do
  subscription=$(curl -fsS "${api}/sub/${subscription_token}/${device_id}" | base64 -d || true)
  link=$(sed -n '/^vless:\/\//{p;q;}' <<<"$subscription")
  [[ -n "$link" ]] && break
  sleep 1
done
[[ -n "$link" ]] || {
  echo 'FAIL: subscription not published after agent acknowledgement' >&2
  exit 1
}
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
echo 'PASS: subscription has the direct exit endpoint and generated Reality credentials'
