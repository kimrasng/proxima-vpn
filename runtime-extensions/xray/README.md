# Pinned Xray history supplier

This directory maintains patches and a small adapter for upstream Xray
`a4a3dff5b20c2faae567b742c172c7b95e75c083` (v26.10.31). It does not vendor
upstream or replace the node-agent subprocess boundary.

## Build and probe

Use Go 1.27.3, or set `XRAY_GO` to that toolchain's executable:

```sh
bash runtime-extensions/xray/build.sh /absolute/output linux arm64 amd64
/absolute/output/proxima-xray-a4a3dff5b20c2faae567b742c172c7b95e75c083-history-v1-linux-arm64 history-capabilities
```

Run the probe on its matching platform. It prints the strict JSONL hello
envelope without starting VPN listeners. Existing `version` and `run`
commands retain their upstream interfaces.

The build verifies source archive, ordered patches, overlay, and shared source
checksums before staging. Every invocation allocates isolated source/module/
compiler caches, uses a relative shared module replacement, and removes its
temporary root on exit. Outputs include the executable, SHA-256 sidecar, and
lock manifest. `XRAY_SOURCE_ARCHIVE` can supply the same checksum-verified
archive without fetching it again. `XRAY_KEEP_STAGE=1` retains a development
stage for the test runners; its printed path is caller-owned.

## Collection contract

Set `PROXIMA_HISTORY_SOCKET` once at process launch to a socket inside a private
0700 directory. The shared exporter owns the 0600 socket and strict
apply/resume/ACK protocol. Collection starts OFF; subsequent toggles do not
rewrite Xray configuration or restart VPN transports.

VLESS, VMess, Trojan, legacy Shadowsocks, single-key SS2022, and multi-user
SS2022 are instrumented after authentication. Shared Shadowsocks credentials
remain shared/unknown account identity. Counters measure proxy payload observed
at the forwarding boundary, not successful delivery or billable traffic.
Direct endpoints are refined only from the real outbound socket; a SOCKS
listener is never reported as the destination.

TCP owners survive OFF/ON with zero-baseline partial snapshots. UDP
history-only destination maps exist exclusively under CollectionScope's
enabled gate. Disable clears them before applied OFF; re-enable discovers new
partial destination IDs from subsequent traffic. Responses that cannot prove
their original request remain response-only observations.

Mux framing is not charged as payload. Retained XUDP associations bind to
authenticated user objects, join accepted callbacks before carrier detach,
and atomically migrate observation segments with gate-held Reparent. Runtime
crash and collection disable never fabricate terminal events.

Authenticated monitored-capable paths disable Linux splice from owner creation,
including while OFF, so a later enable cannot miss already-bypassed Vision
traffic. This is a build-level performance cost, not a route change. The
Vision fixture records OFF and ON 1 MiB round-trip measurements while requiring
both actual direct-mode transitions and exact inner TLS-record byte counts.

## Verification

`tests/staged-check.sh STAGE GO_EXECUTABLE` reconstructs pinned upstream,
applies current patches/overlay, builds the runtime, and runs the native race
suite. Set `XRAY_RUNTIME_RACE=1` to instrument the actual subprocess too.

`tests/linux-check.sh STAGE GO_EXECUTABLE [ARTIFACT_DIRECTORY]` executes the
real arm64 Linux protocol matrix and adapter/mux/dispatcher tests in the
task-owned QA guest. With an artifact directory it verifies and tests those
exact retained release binaries rather than silently rebuilding them.

Commands, failures, final proof logs, reproducibility comparisons, artifact
hashes, and cleanup receipts are under
`.omo/evidence/connection-history/extension/xray/`. A supplier proof is not an
end-to-end API, billing, deployment, or fleet release qualification.
