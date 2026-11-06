#!/usr/bin/env bash
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
(cd "$here" && shasum -a 256 -c patches.sha256)
(cd "$here" && shasum -a 256 -c overlay.sha256)
(cd "$repo" && shasum -a 256 -c "$here/shared.sha256")
output=${1:?usage: bash build.sh OUTPUT_DIRECTORY [GOOS] [GOARCH ...]}
target_os=${2:-linux}
architectures=("${@:3}")
if [[ ${#architectures[@]} == 0 ]]; then architectures=(arm64); fi
mkdir -p "$output"
output=$(cd "$output" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/proxima-xray-build.XXXXXX")
trap 'if [[ ${XRAY_KEEP_STAGE:-0} != 1 ]]; then chmod -R u+w "$work"; rm -rf "$work"; printf "XRAY_BUILD_CLEANUP=%s\n" "$work"; else printf "XRAY_BUILD_STAGE=%s\n" "$work"; fi' EXIT
export TMPDIR="$work/tmp" GOCACHE="$work/cache" GOMODCACHE="$work/modules"
export NODE_COMPILE_CACHE="$work/node" BUN_RUNTIME_TRANSPILER_CACHE_PATH="$work/bun"
export GOWORK=off CGO_ENABLED=0 GOTOOLCHAIN=go1.27.3 GOPROXY=https://proxy.golang.org
mkdir -p "$TMPDIR" "$GOCACHE" "$GOMODCACHE"
commit=a4a3dff5b20c2faae567b742c172c7b95e75c083
if [[ -n ${XRAY_SOURCE_ARCHIVE:-} ]]; then
  cp "$XRAY_SOURCE_ARCHIVE" "$work/source.tar.gz"
else
  curl -fL "https://codeload.github.com/XTLS/Xray-core/tar.gz/$commit" -o "$work/source.tar.gz"
fi
actual=$(shasum -a 256 "$work/source.tar.gz")
[[ ${actual%% *} == 7dba1dd055bdc854a2c5311610872c9bf6315ddb0a741fb8c2206212cec51b53 ]]
tar -xzf "$work/source.tar.gz" -C "$work"
source_dir="$work/Xray-core-$commit"
mkdir "$work/shared"
cp "$repo/pkg/go.mod" "$repo/pkg/go.sum" "$work/shared/"
cp -R "$repo/pkg/nodeprov" "$repo/pkg/connectionhistory" "$repo/pkg/speedtier" "$work/shared/"
cp -R "$here/overlay/." "$source_dir/"
for patch_file in "$here"/patches/*.patch; do
  patch --batch --fuzz=0 -d "$source_dir" -p1 < "$patch_file"
done
cd "$source_dir"
go_bin=${XRAY_GO:-go}
[[ $("$go_bin" env GOVERSION) == go1.27.3 ]]
"$go_bin" mod edit -require=github.com/proximavpn/proxima-vpn/pkg@v0.0.0
"$go_bin" mod edit "-replace=github.com/proximavpn/proxima-vpn/pkg=../shared"
for target_arch in "${architectures[@]}"; do
  name="proxima-xray-$commit-history-v1-$target_os-$target_arch"
  GOOS=$target_os GOARCH=$target_arch "$go_bin" build -trimpath -buildvcs=false -o "$output/$name" ./main
  (cd "$output" && shasum -a 256 "$name" | tee "$name.sha256")
  cp "$here/lock.json" "$output/$name.lock.json"
  printf 'XRAY_BINARY=%s\n' "$output/$name"
done
