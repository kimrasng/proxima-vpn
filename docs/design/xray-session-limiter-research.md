# Xray-core concurrency research and implementation recommendation

## Status and scope

This is a research record and recommendation, not an approved architectural
change, a phase completion claim, or an enabled enforcement implementation.
The existing contract remains distinct authenticated Exit tunnel transports,
not physical devices, source IPs, UUIDs, or destination streams within a tunnel.
Administrative device registration and `max_concurrent` fallback to
`max_devices` are not changed. Explicit zero remains unlimited.

Research used Aside to read official documentation and upstream source. The
source snapshot is XTLS/Xray-core commit
`c77a4d0e4932a53f3eeafafdfef877b56fd660ad`. The release metadata returned by the
research session was inconsistent with other version references, so this
record does not claim a verified latest release or that deployed Exits run
this commit. These are source findings, not live-client measurements.

## Findings

### Online statistics are not tunnel or device counts

`app/stats/online_map.go` stores entries by source IP. Multiple references to
one IP increment a refcount; `Count()` represents distinct IPs.
`GetStatsOnline` and `GetStatsOnlineIpList` therefore cannot distinguish
multiple tunnels sharing an IP and credential. Traffic counters also do not
provide a transport registry.

Online-map accounting occurs in dispatcher paths and follows context
cancellation. It is not an authenticated outer-transport close API. Mux
substreams and idle transports make interpreting it as a tunnel count unsafe.

### One device can open multiple counted transports

The generated VLESS profiles use TCP + REALITY + `xtls-rprx-vision`:

- `api-server/internal/services/subscription/clash.go`, `clashVLESSReality`
- `api-server/internal/services/subscription/singbox.go`, `singboxVLESS`
- `api-server/internal/handlers/subscription.go`, VLESS URI generation

The account subscription shares one account credential across installations
(`api-server/internal/handlers/subscription_account.go`). The generated
profiles do not explicitly request a TCP multiplexing mode.

Upstream VLESS outbound `Process` dials an outer connection for a dispatched
flow. The inspected mihomo VLESS TCP dial path also opens a connection for
each `DialContext` call; that client source was branch-based rather than
SHA-pinned. Vision does not support ordinary TCP requests through VLESS Mux;
its XUDP path is separate. Thus one browsing device can have several or many
simultaneous outer transports. The exact distribution is not measured here.

**A three-transport limit is not a three-device limit.** With the current
profiles it can reject the fourth concurrent flow on a single device. The
transport contract can be enforced, but the product must label it accurately.
It must not be silently reinterpreted as a device count or changed to IP
cohorts to make a small limit appear to work.

### Access logging and later routing-rule insertion cannot gate first contact

In `app/dispatcher/default.go`, `routedDispatch` selects the route before
recording the accepted access log and dispatching to the outbound. Installing
a source-port rule after observing the access log cannot retroactively change
that dispatch. A new TCP retry has no guaranteed source-port continuity.

The current source+port declarations do not match a connection acceptance
generation inside Xray. The application-side session token is metadata, not a
native Xray routing condition. Tuple reuse can match stale rules. On gRPC or
XHTTP, logical transport streams can share a physical network tuple, which
also defeats socket-tuple identity. Those protocols need a separately stated
counting contract if enabled.

RoutingService offers rule/statistics operations, not a synchronous external
admission callback. At the inspected snapshot, the `sib` CLI builds a routing
rule through AddRule; it is not a confirmed kill-established-transport API.

### The useful lifecycle is inside the authenticated inbound

VLESS inbound `Handler.Process` authenticates the request, attaches
`inbound.User`, checks protocol/flow eligibility, and then writes the response
and dispatches. An acquisition hook can be inserted after authentication and
flow validation but before response/dispatch, denying the same connection
without a deny-first learning/retry loop.

`tcpWorker.callback` calls `Process` and then `cancel()` followed by
`conn.Close()`. A defer in `Process`, or `context.AfterFunc` alone, runs too
early to prove socket closure. Release must be ordered after actual connection
closure and after forwarding has stopped. Error/panic/early-return paths and
transport-specific adapters need explicit tests; this is not merely a small
patch with no lifecycle work.

The upstream diagnostic `session.NewID()` is a 32-bit random value, not a
fleet-wide reservation identity. Use Exit identity, a core-process generation,
and a sufficiently strong per-transport nonce. Authentication of the node or
local IPC channel, not an unkeyed token hash, authorizes reservation operations.

There is no native configuration feature for the complete proposed lifecycle
in the inspected policy/routing/stats APIs. A version-pinned custom core build
with a narrow maintained patch is the clearest implementation candidate.
Embedding/replacing features without patching authentication and close paths
remains unverified, particularly for Mux.

## Current workspace state

Useful existing pieces include account/plan entitlement checks, exact-transport
reservation primitives, shared token generation, admin observation, and
receipt/fence-based reconciliation. The scheduler now invokes reconciliation
for active accounts, including unlimited accounts, with a per-account throttle.

However:

- Production node-agent session admission/enter/leave callbacks are unwired.
- Authentication evidence currently comes from access logs joined with socket
  polling. `AcceptedAt` is a log-observation timestamp, not an exact core
  acceptance event. Peer reuse and log loss remain observation hazards.
- Local SOCKS associations are destination-stream lifetimes, not outer tunnel
  lifetimes. They must not control slot release.
- The API admission endpoint validates an already-published fresh report.
  A pre-dispatch hook needs a new authenticated acquire contract; simply
  calling this report-based endpoint from the hook will not establish first
  contact.
- DB/Redis-backed admission and reconciliation tests have not run in the
  available environment. Passing compile/unit/race/lint checks is not proof
  of lifecycle enforcement or supported-client behavior.
- Redis release correctness, replay races, and failover/durability remain
  acceptance work. Automatic session enforcement stays disabled.

The current installer defaults to an upstream latest-at-install-time binary
unless `XRAY_VERSION` is supplied. Runtime updates download an admin-selected
upstream release and validate that `-version` works. A future custom hook can
be silently lost through that stock update path without capability gating.

## Recommended strict-session architecture

```text
Client -> optional Entry relay -> Exit custom Xray
                                  |
                                  | authenticated transport: acquire once
                                  v
                               node-agent
                                  |
                                  | account-wide atomic grant
                                  v
                             control-plane store

Granted -> normal routing/egress and local bandwidth accounting
Closed and stopped -> exact fenced release
Lost release -> complete generation-bound registry reconciliation
```

### Core and local agent

A core-side transport handle is created per supported inbound connection and
bound to its authenticated credential. After protocol validation, a bounded
local IPC acquire request reaches the node-agent, which forwards a mutually
authenticated account-wide reservation request. It grants no forwarding until
that reservation succeeds. New requests fail closed on unavailable authority.

Store a core generation and transport nonce independently of IP, port, and
log timestamps. Propagate the granted handle into Mux substreams; acquire only
once at the outer boundary, never in each substream dispatcher. Any detached
XUDP path requires separate handling to prove that forwarding cannot outlive
its owning grant. Do not claim support before testing that path.

A core-side registry holds the connection/forwarding owner and supports exact
termination. Close stops forwarding and closes the transport; only then is an
exact release event sent through a retryable outbox. Do not send release merely
because one SOCKS destination stream ended. If the agent crashes while core
continues, do not claim an empty registry or a new core generation.

Keep existing bandwidth/egress functions separate. Session-specific SOCKS
outbounds and source-port rules are not required as the admission mechanism.
The initial implementation should not also rewrite unrelated egress policy.

### Account-wide authority and recovery

Keep durable slots initially: no silent TTL expiry. Acquire is atomic per
account and retry-safe; close carries the matching core generation and grant
fence. Delayed close/replay must not release a later grant. Multiple Exits
share one account capacity; Entry is not an additional owner. Original-source
NAT attribution is optional diagnostics, not necessary to count sessions.

Use generation-bound, sequenced complete registry reports for repair. Retain
conservative independent-report absence confirmation. A new generation only
fences the old one after the old core is proven stopped; an agent restart or
missed heartbeat alone is not that proof.

On a partition, a durable-slot policy can preserve existing granted sessions
while denying new ones. It favors strict capacity over immediate recovery:
slots may stay occupied until a close, complete report, or verified operational
fence arrives. Explain that availability trade-off in the UI.

A later lease design can improve recovery only if the Exit demonstrably stops
forwarding before the control plane reuses the slot, including clock, suspend,
watchdog and Vision splice behavior. A TTL disappearing while an isolated
Exit still forwards can over-admit. Do not enable automatic TTL expiry or
new-session fail-open grace under a strict global limit.

Redis loss/failover can also erase reservations while Exits remain live.
Admission must be quarantined until durable grants/full registries are
recovered, or use another adequately durable authority. A successful Lua call
alone does not establish failover safety.

### Efficiency and release management

- One central acquisition per counted transport, not per byte or Mux stream.
  A normal Vision client may still create many transports; measure this load.
- Local bandwidth enforcement remains in the data path, without a central
  per-chunk authorizer.
- Batch close retries and registry reports per node; preserve event ordering
  and generation/sequence fences.
- Replace repeated account-by-Exit polling with node-oriented registry
  ingestion and account indexes when measured load justifies it.
- Bound pending handshakes, registry size, IPC queue and acquire concurrency.
  Use short-lived negative capacity results and retry backoff to suppress
  reconnect storms without granting unreserved sessions.
- Pin core artifacts and verify build provenance plus a versioned
  authentication/close/registry capability. Do not infer it from a version
  string. Reject stock upgrades when session enforcement is requested.

## Device management is a separate product decision

If the desired product is actually "up to three registered devices", support
explicit per-installation credentials and names. Multiple connections using
one installation credential can then share a device-level allocation while
transport counts remain diagnostic or a separate abuse limit. This requires
an approved change to the present shared-account-credential workflow; it is
not part of the already-agreed session counting contract.

A per-device UUID is a registered credential, not proof of a physical machine.
Copying it allows sharing. Stronger installation binding needs a controlled
client with a non-exportable key or equivalent explicit enrollment; arbitrary
third-party VLESS profile imports cannot establish that proof. Do not revive
`x-hwid` or use IP heuristics as physical-device identity.

## Verification before enablement

Measure real client behavior first (mihomo/Clash Verge Rev and sing-box,
importing the generated profiles). Record outer transports for browsing,
streaming, UDP, idle, reconnection, and health checks; no fixed connection count
is asserted by this research.

For the agreed session contract, verify same UUID/IP separate transports,
multiple Exits racing for the last slot, no destination connection or payload
before grant, Mux substreams not reacquiring, exact post-stop release, network
switches, core/agent crashes, lost replies, late/replayed events, partitions,
Redis failover, registry gaps and stock-core upgrade rejection. Start with the
current VLESS TCP/REALITY profile. Gate unsupported protocols rather than
advertising fleet-wide enforcement. Begin observe-only and enable on a canary
only after these checks; do not mark PHS4 complete prematurely.

## Primary sources and limitations

Xray source links are pinned to the research snapshot:

- [IP online map](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/app/stats/online_map.go)
- [Statistics RPC implementation](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/app/stats/command/command.go)
- [VLESS inbound authentication and dispatch](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/proxy/vless/inbound/inbound.go)
- [VLESS outbound dial path](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/proxy/vless/outbound/outbound.go)
- [Inbound worker close ordering](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/app/proxyman/inbound/worker.go)
- [Mux dispatcher wiring](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/app/proxyman/inbound/always.go)
- [Route decision and access log ordering](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/app/dispatcher/default.go)
- [Routing RPC contract](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/app/router/command/command.proto)
- [Source-IP rule CLI](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/main/commands/all/api/source_ip_block.go)
- [Diagnostic session IDs](https://github.com/XTLS/Xray-core/blob/c77a4d0e4932a53f3eeafafdfef877b56fd660ad/common/session/session.go)
- [Official policy documentation](https://xtls.github.io/en/config/policy.html)
- [Official VLESS outbound documentation](https://xtls.github.io/en/config/outbounds/vless.html)
- [Mihomo VLESS adapter, unpinned branch](https://github.com/MetaCubeX/mihomo/blob/Meta/adapter/outbound/vless.go)

The external research did not complete a deployed-version check, real traffic
measurement, exhaustive transport-adapter audit, or a tested library-embedding
alternative. It does not substantiate switching server engines. Current graph
phase mapping validation still has six previously observed deprecated-target
findings; no graph entity is marked resolved by this research.
