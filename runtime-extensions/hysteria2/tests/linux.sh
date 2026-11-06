#!/usr/bin/env bash
set -euo pipefail
umask 077
tests=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$tests/../../.." && pwd)
evidence=${1:?usage: linux.sh EVIDENCE_DIRECTORY}
mkdir -p "$evidence"
evidence=$(cd "$evidence" && pwd)
lima="$repo/.omo/evidence/connection-history/environment/owned-lima-01a1b08e.sh"
vm=proxima-history-01a1b08e
host=$(mktemp -d /tmp/proxima-hysteria-01a1b0d8.XXXXXX)
export TMPDIR="$host/tmp" GOTMPDIR="$host/tmp" GOCACHE="$host/gocache" GOMODCACHE="$host/modcache"
export NODE_COMPILE_CACHE="$host/nodecache" BUN_RUNTIME_TRANSPILER_CACHE_PATH="$host/buncache"
mkdir -p "$TMPDIR"
guest=
cleanup() {
  rc=$?
  if [[ -n "$guest" ]]; then
    bash "$lima" shell --workdir=/var/tmp "$vm" sh -c \
      'chmod -R u+w "$1"; rm -rf "$1"' sh "$guest"
    printf 'CLEANED_GUEST %s\n' "$guest"
  fi
  chmod -R u+w "$host"
  rm -rf "$host"
  printf 'CLEANED_HOST %s\n' "$host"
  exit "$rc"
}
trap cleanup EXIT
guest=$(bash "$lima" shell --workdir=/var/tmp "$vm" mktemp -d /var/tmp/proxima-hysteria-01a1b0d8.XXXXXX)
printf 'OWNED_HOST %s\nOWNED_GUEST %s\n' "$host" "$guest"
COPYFILE_DISABLE=1 tar --no-xattrs --no-fflags -czf "$host/input.tar.gz" -C "$repo" runtime-extensions/hysteria2 pkg
(cd "$host"; shasum -a 256 input.tar.gz)
url=https://proxy.golang.org/golang.org/toolchain/@v/v0.0.1-go1.26.8.linux-arm64.zip
curl --fail --location "$url" -o "$host/toolchain.zip"
printf '%s  %s\n' 02b36b6787b3ac767668f630e2998df45a3b1f9ffee08d1bf3ef6487cd658d99 "$host/toolchain.zip" | shasum -a 256 -c -
/usr/bin/ditto -x -k "$host/toolchain.zip" "$host/toolchain"
COPYFILE_DISABLE=1 tar --no-xattrs --no-fflags -czf "$host/toolchain.tar.gz" \
  -C "$host/toolchain/golang.org/toolchain@v0.0.1-go1.26.8.linux-arm64" .
bash "$lima" copy "$host/input.tar.gz" "$vm:$guest/input.tar.gz"
bash "$lima" copy "$host/toolchain.tar.gz" "$vm:$guest/toolchain.tar.gz"
bash "$lima" copy "$tests/guest.sh" "$vm:$guest/guest.sh"
set +e
bash "$lima" shell --workdir=/var/tmp "$vm" bash "$guest/guest.sh" "$guest" 2>&1 | tee "$evidence/linux.log"
status=${PIPESTATUS[0]}
set -e
# Preserve failure evidence too. Nothing inside the VM's shared state is removed.
bash "$lima" copy -r "$vm:$guest/result" "$evidence/artifacts"
exit "$status"
