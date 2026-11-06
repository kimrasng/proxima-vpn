#!/bin/sh
set -eu
exec "${XRAY_QEMU:?task-owned qemu-x86_64-static required}" \
  "${XRAY_AMD64_BINARY:?exact retained amd64 artifact required}" "$@"
