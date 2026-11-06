# Pinned Hysteria precise-history extension

Baseline: `ca8fbd874fd6ca82413948de6dc5924405e103e1` (`app/v2.13.0`).
Toolchain: Go 1.26.8. No upstream source tree is vendored.

`patches/0001-precise-history.patch` changes the application startup, core config,
authenticated QUIC owner, TCP request owner, and UDP association lifetime.
`overlay/` supplies the small additional Go implementations and regression tests.
The application imports the existing shared `pkg/connectionhistory` exporter and
`pkg/nodeprov` wire contract through a staging-only module replacement.

## Build and proof

```sh
bash runtime-extensions/hysteria2/build.sh /absolute/output/directory
bash runtime-extensions/hysteria2/tests/linux.sh \
  .omo/evidence/connection-history/extension/hysteria2/off-retention-recovery-final
```

`build.sh` fetches and verifies the full commit in its own temporary directory,
checks patch/overlay and shared-source digests, preserves upstream app/core/extras
siblings and checksums, runs server race tests and vet, and builds linux/amd64 and
linux/arm64 with trimpath, buildvcs=false, and fixed version metadata. It removes
only its own staging/cache root. `HYSTERIA_GO` selects a bootstrap Go executable;
GOTOOLCHAIN pins Go 1.26.8 without modifying global Go settings.

The Linux wrapper is for this checkout's retained, isolated Lima VM. It transfers
an explicit source snapshot, obtains the pinned toolchain from the official Go
module proxy into owned roots, runs the real-binary scenarios, builds again from
clean staging, compares both architecture artifacts, retains evidence, and removes
its own guest and host staging. It does not stop or reconfigure the VM. The source
mount is deliberately unused because it points to a different worktree.

`lock.json`, `sources.sha256`, and `shared.sha256` pin the maintained inputs.
After an intentional shared exporter update, regenerate and review these digests
and repeat the clean-build/live-proof command; do not bypass a checksum failure.
Output contains architecture-specific SHA-256 files, source manifests, and a
supplier-input manifest covering build scripts and runtime proof tests too.
The older `final/` artifacts predate the OFF-retention correction. The canceled
`off-retention-final/` run used a superseded shared-source digest. Both remain
provisional historical evidence.

## Runtime integration

The normal command stays `hysteria server --config ...`.

- Set `PROXIMA_HISTORY_SOCKET` at process launch to a private Unix socket path.
  The shared exporter enforces directory 0700 and socket 0600.
- Optional `PROXIMA_HISTORY_PRODUCER_ID` identifies multiple node-local instances;
  the default is `hysteria2`.
- Optional `PROXIMA_HISTORY_INBOUND_TAG` selects the binding-manifest tag; the
  default is `hysteria2`.
- `hysteria history-capabilities` emits the shared machine-readable hello schema.
- Collection begins OFF. The collector uses shared hello/resume/apply/ACK
  framing. ON/OFF never modifies Hysteria config or restarts the runtime.

Bindings use the authenticator's accepted user ID, not a client-supplied password.
Synthetic proof credentials use the upstream `username:password` format. The
product subscription mismatch belongs to the separate integration lane.

## Measurement and lifecycle

One tunnel owner is allocated under the authentication mutex for each successfully
authenticated QUIC connection. Repeated authentication does not allocate another.
Each parsed TCP request receives an independent owner, including identical targets
on the same connection. Original targets survive sniff rewriting; effective host
metadata is refined without changing identity or parent.

TCP counts source payload reads, including nonzero reads accompanied by an error.
Sniff putback is counted once when reintroduced, before the outbound write.
On first copy completion, existing I/O is closed, blocked writes are released by
their deadline, and both copy completions are joined before finalization. Tunnel
closure joins TCP children and the complete UDP manager before its own end.

UDP counts complete logical requests after reassembly and replies before response
fragmentation. Flows are per original target and association generation.
An exact numeric response endpoint is assigned only when one request can explain
it, no unresolved alias makes the join ambiguous, and the association has been
continuously observed since creation. Other responses receive a
response-only flow with null requested fields. Actual response endpoints are
recorded; a SOCKS listener is never substituted for the remote destination.

UDP ends are activity/observation closure, not application disconnect. Idle expiry
uses `activity_expired`; transport/error closure uses `observation_segment_closed`.
Close stops admission, closes I/O, and finalizes from outside the receive goroutine
only after all Feed/write/receive work has quiesced.

EventLogger, TrafficLogger, their aggregate byte definitions, and enforcement
callbacks remain present. History counters do not replace billing counters.
The history build uses observed copy callbacks from owner creation even while OFF
so a held TCP flow can be enabled later. TCP's live runtime owner remains while
OFF; enable resets its epoch baseline and marks it partial.

UDP destination maps are history-only, not live dataplane state. One shared
`CollectionScope` per live association gates every request/effective/response map
lookup and mutation with Apply. OFF never invokes those callbacks. Disable emits
nonterminal samples, silently retires scoped destination owners, and drops all
three maps before acknowledging OFF. Only the scope and authenticated parent
handles remain; forwarding and its existing bounded ACL cache are independent.

On re-enable, no forgotten destinations are replayed. The first new observation
creates a fresh zero-baseline partial segment under the same authenticated tunnel.
An initially-OFF association behaves the same way. Since forgotten requests could
alias a later reply source, replies on these partial associations are conservatively
response-only. Disable never manufactures flow ends.

Scope callbacks/reset run synchronously under the shared observer mutex: no I/O,
waits, runtime locks, goroutines, or ordinary lock-taking observer/flow calls.
They use only the supplied gate operations. Error logging and forwarding remain
outside that mutex. Association Close follows existing callback quiescence,
finalizes current ON segments, unregisters the scope and releases the maps.

Runtime memory replay is not crash-durable. A killed runtime cannot supply final
bytes or ends. The agent must report loss for the vanished boot; this extension
does not invent a crash-final snapshot. Endpoint delivery and client-button time
are not measured.

## Known upstream boundary

The pinned UDP serialization buffer is 4096 bytes including the Hysteria address
and header. A 4096-byte *payload* is silently dropped by the upstream client
before fragmentation. The proof uses 3000 payload bytes, larger than its 1200-byte
datagram frame and small enough for serialization. This extension preserves that
existing protocol boundary rather than changing unrelated client behavior.
