#!/usr/bin/env bash
set -euo pipefail
umask 077
d=${1:?usage: guest.sh OWNED_GUEST_ROOT}
case "$d" in /var/tmp/proxima-hysteria-01a1b0d8.*) ;; *) exit 2 ;; esac
export TMPDIR="$d/tmp" GOTMPDIR="$d/tmp" GOCACHE="$d/gocache" GOMODCACHE="$d/modcache"
export NODE_COMPILE_CACHE="$d/nodecache" BUN_RUNTIME_TRANSPILER_CACHE_PATH="$d/buncache"
export HYSTERIA_GO="$d/go/bin/go"
mkdir -p "$TMPDIR" "$d/go" "$d/input" "$d/result"
(cd "$d"; shasum -a 256 input.tar.gz)
tar -xzf "$d/toolchain.tar.gz" -C "$d/go"
chmod +x "$d/go/bin/go" "$d/go/bin/gofmt" "$d/go/pkg/tool/linux_arm64/"*
tar -xzf "$d/input.tar.gz" -C "$d/input"
uname -a
"$HYSTERIA_GO" version
export HYSTERIA_PROOF=1
bash "$d/input/runtime-extensions/hysteria2/build.sh" "$d/result/first"
export HYSTERIA_PROOF=0 HYSTERIA_TEST=0
bash "$d/input/runtime-extensions/hysteria2/build.sh" "$d/result/second"
for arch in amd64 arm64; do
  name="proxima-hysteria2-ca8fbd874fd6ca82413948de6dc5924405e103e1-history-v1-linux-$arch"
  cmp "$d/result/first/$name" "$d/result/second/$name"
  printf 'REPRODUCIBLE %s\n' "$arch"
done
for manifest in extension-sources.sha256 shared-sources.sha256 supplier-inputs.sha256; do
  cmp "$d/result/first/$manifest" "$d/result/second/$manifest"
done
printf 'INPUT_MANIFESTS_IDENTICAL\n'
"$d/result/first/proxima-hysteria2-ca8fbd874fd6ca82413948de6dc5924405e103e1-history-v1-linux-arm64" history-capabilities \
  > "$d/result/first/capabilities.json"
printf 'LINUX_PRECISE_HISTORY_PASS\n'
