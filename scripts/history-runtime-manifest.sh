#!/bin/bash
set -euo pipefail

# Print a deployment manifest from the owners' real build/checksum outputs.
# Usage: bash scripts/history-runtime-manifest.sh XRAY_OUTPUT HYSTERIA_OUTPUT
# Copy the artifact directories to the node before invoking install.sh.
[[ $# == 2 ]] || { echo "usage: $0 XRAY_OUTPUT HYSTERIA_OUTPUT" >&2; exit 2; }
xray=$(cd "$1" && pwd)
hysteria=$(cd "$2" && pwd)
records=()
for runtime in xray hysteria2; do
    if [[ "$runtime" == xray ]]; then
        directory=$xray
        commit=a4a3dff5b20c2faae567b742c172c7b95e75c083
        build=xray-a4a3dff5-history-v1
        version=v26.10.31
    else
        directory=$hysteria
        commit=ca8fbd874fd6ca82413948de6dc5924405e103e1
        build=hysteria2-ca8fbd874fd6-history-v1
        version=v2.13.0
    fi
    for arch in amd64 arm64; do
        file="proxima-${runtime}-${commit}-history-v1-linux-${arch}"
        read -r checksum recorded < "$directory/$file.sha256"
        [[ "$recorded" == "$file" && "$checksum" =~ ^[0-9a-f]{64}$ ]] ||
            { echo "invalid artifact checksum receipt: $file" >&2; exit 1; }
        if command -v sha256sum >/dev/null 2>&1; then
            (cd "$directory" && sha256sum -c "$file.sha256" >&2)
        else
            (cd "$directory" && shasum -a 256 -c "$file.sha256" >&2)
        fi
        records+=("$(jq -cn --arg runtime "$runtime" --arg commit "$commit" --arg version "$version" \
            --arg build "$build" --arg arch "$arch" --arg path "$directory/$file" --arg sha256 "$checksum" \
            '{runtime:$runtime,commit:$commit,version:$version,build:$build,os:"linux",arch:$arch,path:$path,sha256:$sha256}')")
    done
done
printf '%s\n' "${records[@]}" | jq -s '{schema_version:1,artifacts:.}'
