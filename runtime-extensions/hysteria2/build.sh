#!/usr/bin/env bash
set -euo pipefail
umask 077

extension=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$extension/../.." && pwd)
out=${1:?usage: build.sh OUTPUT_DIRECTORY}
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/proxima-hysteria-build.XXXXXX")
trap 'chmod -R u+w "$work"; rm -rf "$work"' EXIT
export TMPDIR="$work/tmp" GOTMPDIR="$work/tmp" GOCACHE="$work/gocache"
export GOMODCACHE="$work/modcache" GOPATH="$work/gopath" GOWORK=off
export NODE_COMPILE_CACHE="$work/nodecache"
export BUN_RUNTIME_TRANSPILER_CACHE_PATH="$work/buncache"
export GOTOOLCHAIN=go1.26.8 GOSUMDB=sum.golang.org GOPROXY=https://proxy.golang.org
export GOFLAGS=-modcacherw
export GOMAXPROCS=2
mkdir -p "$TMPDIR"
(cd "$extension"; shasum -a 256 -c sources.sha256)
go_cmd=${HYSTERIA_GO:-go}
test "$("$go_cmd" env GOVERSION)" = go1.26.8

pin=ca8fbd874fd6ca82413948de6dc5924405e103e1
GIT_MASTER=1 git init -q "$work/upstream"
GIT_MASTER=1 git -C "$work/upstream" fetch -q --depth 1 https://github.com/HyNetworks/hysteria.git "$pin"
GIT_MASTER=1 git -C "$work/upstream" checkout -q --detach FETCH_HEAD
test "$(GIT_MASTER=1 git -C "$work/upstream" rev-parse HEAD)" = "$pin"
for patch in "$extension"/patches/*.patch; do
  GIT_MASTER=1 git -C "$work/upstream" apply --check "$patch"
  GIT_MASTER=1 git -C "$work/upstream" apply "$patch"
done
cp -R "$extension/overlay/." "$work/upstream/"
mkdir -p "$work/shared"
cp -R "$repo/pkg/." "$work/shared/"

(
  cd "$work/shared"
  find . -type f -name '*.go' -o -name go.mod -o -name go.sum |
    LC_ALL=C sort | while IFS= read -r f; do shasum -a 256 "$f"; done
) > "$out/shared-sources.sha256"
shasum -a 256 "$out/shared-sources.sha256"
(cd "$out"; shasum -a 256 -c "$extension/shared.sha256")
(
  cd "$extension"
  find patches overlay -type f | LC_ALL=C sort |
    while IFS= read -r f; do shasum -a 256 "$f"; done
) > "$out/extension-sources.sha256"
(
  cd "$extension"
  find . -type f | LC_ALL=C sort |
    while IFS= read -r f; do shasum -a 256 "$f"; done
) > "$out/supplier-inputs.sha256"

if [[ ${HYSTERIA_TEST:-1} == 1 ]]; then
  (cd "$work/upstream/core"; CGO_ENABLED=1 "$go_cmd" test -mod=mod -race -shuffle=on -count=1 -timeout=180s -v ./server)
  (cd "$work/upstream/core"; "$go_cmd" vet -mod=mod ./server)
  printf 'CORE_RACE_VET_PASS\n'
fi
for arch in ${HYSTERIA_ARCHES:-amd64 arm64}; do
  name="proxima-hysteria2-$pin-history-v1-linux-$arch"
  ld="-X github.com/apernet/hysteria/app/v2/cmd.appVersion=v2.13.0-history-v1"
  ld="$ld -X github.com/apernet/hysteria/app/v2/cmd.appDate=2026-10-05T00:00:00Z"
  ld="$ld -X github.com/apernet/hysteria/app/v2/cmd.appCommit=$pin"
  ld="$ld -X github.com/apernet/hysteria/app/v2/cmd.appToolchain=go1.26.8"
  ld="$ld -X github.com/apernet/hysteria/app/v2/cmd.appType=proxima-history-v1"
  ld="$ld -X github.com/apernet/hysteria/app/v2/cmd.appPlatform=linux"
  ld="$ld -X github.com/apernet/hysteria/app/v2/cmd.appArch=$arch"
  ld="$ld -X github.com/apernet/hysteria/app/v2/cmd.libVersion=v0.63.1-0.20261004180939-a10df75c260c"
  (cd "$work/upstream/app"; GOOS=linux GOARCH="$arch" CGO_ENABLED=0 "$go_cmd" build -mod=mod -trimpath -buildvcs=false -ldflags "$ld" -o "$out/$name" .)
  (cd "$out"; shasum -a 256 "$name") > "$out/$name.sha256"
  printf 'BUILT %s\n' "$name"
done
if [[ ${HYSTERIA_PROOF:-0} == 1 ]]; then
  cp "$extension/tests/"*_test.go "$work/upstream/core/server/"
  (cd "$work/upstream/core"; HYSTERIA_BINARY="$out/proxima-hysteria2-$pin-history-v1-linux-arm64" HYSTERIA_EVIDENCE="$out" CGO_ENABLED=1 "$go_cmd" test -mod=mod -race -count=1 -timeout=180s -v -run '^TestHistoryBinary' ./server)
fi
