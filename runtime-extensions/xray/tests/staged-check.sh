#!/usr/bin/env bash
set -euo pipefail
work=${1:?owned build staging root required}
go_bin=${2:?pinned Go executable required}
here=$(cd "$(dirname "$0")/.." && pwd)
repo=$(cd "$here/../.." && pwd)
export TMPDIR="$work/tmp" GOCACHE="$work/cache" GOMODCACHE="$work/modules"
export GOWORK=off GOTOOLCHAIN=local GOPROXY=https://proxy.golang.org
source_dir="$work/Xray-core-a4a3dff5b20c2faae567b742c172c7b95e75c083"
test -d "$source_dir"
test "$("$go_bin" env GOVERSION)" = go1.27.3
tar -xzf "$work/source.tar.gz" -C "$work"
for patch_file in "$here"/patches/*.patch; do
  patch --batch --fuzz=0 -d "$source_dir" -p1 < "$patch_file"
done
cp -R "$repo/pkg/speedtier" "$repo/pkg/connectionhistory" "$repo/pkg/nodeprov" "$work/shared/"
cp -R "$here/overlay/." "$source_dir/"
mkdir -p "$source_dir/historytests"
cp "$here"/tests/*_test.go "$source_dir/historytests/"
cd "$source_dir"
"$go_bin" mod edit -require=github.com/proximavpn/proxima-vpn/pkg@v0.0.0
"$go_bin" mod edit "-replace=github.com/proximavpn/proxima-vpn/pkg=../shared"
if [[ ${XRAY_RUNTIME_RACE:-0} == 1 ]]; then
  CGO_ENABLED=1 "$go_bin" build -race -trimpath -buildvcs=false -o "$work/xray-history" ./main
else
  CGO_ENABLED=0 "$go_bin" build -trimpath -buildvcs=false -o "$work/xray-history" ./main
fi
XRAY_HISTORY_BINARY="$work/xray-history" CGO_ENABLED=1 "$go_bin" test -race -count=1 -timeout=360s -v ./historytests ./common/history ./common/mux ./app/dispatcher
