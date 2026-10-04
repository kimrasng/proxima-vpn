#!/usr/bin/env bash
set -euo pipefail

# Opt-in only. No application process, deployment, Docker daemon or live service
# is required. Tool binaries are supplied by the caller, never globally installed.
root=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
: "${BANDWIDTH_IMPLEMENTATIONS_READY:?Set to 1 only after limiter/generator implementations are ready}"
[[ "$BANDWIDTH_IMPLEMENTATIONS_READY" == 1 ]] || { echo 'Implementations are not marked ready' >&2; exit 2; }
: "${BANDWIDTH_XRAY:?Absolute path to the downloaded real Xray executable required}"
[[ -x "$BANDWIDTH_XRAY" && "$BANDWIDTH_XRAY" == /* ]] || { echo 'BANDWIDTH_XRAY must be an absolute executable path' >&2; exit 2; }
go_binary=${BANDWIDTH_GO:-go}
work=$(mktemp -d "${TMPDIR:-/tmp}/device-bandwidth-evidence.XXXXXX")
export BANDWIDTH_EVIDENCE="$work/evidence.json"
echo "Sanitized evidence: $BANDWIDTH_EVIDENCE"
"$BANDWIDTH_XRAY" version | head -n 1 > "$work/xray-version.txt"
cd "$root"
export GOWORK=off
# Separate internal-module adapter calls the actual API config generator but
# cannot read live services: it accepts only JSON stdin and returns JSON stdout.
(cd generator && "$go_binary" build -mod=mod -o "$work/generate-device-egress" .)
export BANDWIDTH_GENERATOR="$work/generate-device-egress"
# A narrow, bounded test package. Configs and ephemeral private keys are removed
# by testing.T.TempDir; evidence deliberately excludes configs and key material.
"$go_binary" test -mod=mod -count=1 -timeout=180s -v "$@" . 2>&1 | tee "$work/test.log"
