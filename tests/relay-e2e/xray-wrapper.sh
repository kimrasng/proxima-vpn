#!/usr/bin/env bash
set -euo pipefail

real_xray=/usr/local/libexec/xray
args=("$@")

for index in "${!args[@]}"; do
  if [[ "${args[$index]}" != /etc/node-agent/xray-config.json ]]; then
    continue
  fi
  patched=/tmp/relay-e2e-xray-config.json
  jq '
    .outbounds |= map(
      if .protocol == "freedom" then
        .settings = ((.settings // {}) + {
          finalRules: ([{
            action: "allow",
            ip: ["127.0.0.1/32"],
             port: "9090,9091"
          }] + (.settings.finalRules // []))
        })
      else . end
    )
  ' "${args[$index]}" > "$patched"
  args[$index]=$patched
  echo "relay E2E: applied test-only freedom allow for 127.0.0.1:9090" >&2
  break
done

exec "$real_xray" "${args[@]}"
