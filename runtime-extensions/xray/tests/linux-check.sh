#!/usr/bin/env bash
set -euo pipefail
work=${1:?owned build stage required}
go_bin=${2:?pinned Go compiler required}
artifacts=${3:-}
here=$(cd "$(dirname "$0")/.." && pwd)
repo=$(cd "$here/../.." && pwd)
wrapper="$repo/.omo/evidence/connection-history/environment/owned-lima-01a1b08e.sh"
vm=proxima-history-01a1b08e
guest="/var/tmp/proxima-xray-01a1b0a6-$(date +%s)-$$"
source_dir="$work/Xray-core-a4a3dff5b20c2faae567b742c172c7b95e75c083"
export TMPDIR="$work/tmp" GOCACHE="$work/cache" GOMODCACHE="$work/modules"
export GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0
cd "$source_dir"
if [[ -n "$artifacts" ]]; then
  for arch in arm64 amd64; do
    name="proxima-xray-a4a3dff5b20c2faae567b742c172c7b95e75c083-history-v1-linux-$arch"
    (cd "$artifacts" && shasum -a 256 -c "$name.sha256")
    cp "$artifacts/$name" "$work/xray-linux-$arch"
  done
else
  GOOS=linux GOARCH=arm64 "$go_bin" build -trimpath -buildvcs=false -o "$work/xray-linux-arm64" ./main
  GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -buildvcs=false -o "$work/xray-linux-amd64" ./main
fi
GOOS=linux GOARCH=arm64 "$go_bin" test -c -trimpath -buildvcs=false -o "$work/historytests-linux-arm64" ./historytests
for package in common/history common/mux app/dispatcher; do
  GOOS=linux GOARCH=arm64 "$go_bin" test -c -trimpath -buildvcs=false -o "$work/${package//\//-}-tests-linux-arm64" "./$package"
done
shasum -a 256 "$work/xray-linux-arm64" "$work/xray-linux-amd64" "$work/historytests-linux-arm64"
# Copy exact built source and shared package, not the VM's stale repository mount.
COPYFILE_DISABLE=1 tar --no-xattrs -czf "$work/linux-inputs.tar.gz" -C "$work" \
  Xray-core-a4a3dff5b20c2faae567b742c172c7b95e75c083 shared \
  xray-linux-arm64 xray-linux-amd64 historytests-linux-arm64 \
  common-history-tests-linux-arm64 common-mux-tests-linux-arm64 app-dispatcher-tests-linux-arm64
shasum -a 256 "$work/linux-inputs.tar.gz"
bash "$wrapper" shell --workdir=/var/tmp "$vm" mkdir -m 700 "$guest"
printf 'XRAY_LINUX_SCRATCH=%s\n' "$guest"
bash "$wrapper" copy "$work/linux-inputs.tar.gz" "$vm:$guest/inputs.tar.gz"
bash "$wrapper" shell --workdir=/var/tmp "$vm" bash -c \
  'set -euo pipefail; cd "$1"; tar -xzf inputs.tar.gz; uname -a; sha256sum xray-linux-* historytests-linux-arm64; ./xray-linux-arm64 history-capabilities; ./xray-linux-arm64 version; XRAY_HISTORY_BINARY="$PWD/xray-linux-arm64" ./historytests-linux-arm64 -test.v -test.timeout=360s; for unit in common-history common-mux app-dispatcher; do ./"$unit"-tests-linux-arm64 -test.v -test.timeout=60s; done' \
  bash "$guest"
printf 'XRAY_LINUX_SCRATCH=%s\n' "$guest"
