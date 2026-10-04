# Approved design proposal: Single subscription URL, HWID-issued UUIDs, and UUID concurrency

Status: Proposed for approval and planning registration. No implementation starts from this document alone.

## Product contract

- Each account has one stable subscription URL: `/sub/{sub_token}`.
- A subscription refresh with a valid `x-hwid` identifies one logical client installation.
- The first valid, previously unseen HWID creates one server-side device record and one Xray UUID. A later request with the same normalized HWID receives configuration with the same UUID.
- An active connection slot means one currently-online Xray UUID, not one TCP connection, UDP flow, source IP, exit node, or subscription refresh request.
- One UUID may carry many VLESS/Vision/XUDP connections and still consume exactly one account concurrency slot.
- Account concurrency equals the set of distinct online UUIDs across all fresh Exit reports.
- A plan’s `max_concurrent` governs slots. A value of zero/null retains the existing unlimited convention.
- Per-device bandwidth policy applies to each issued UUID. It is not shared across the account and not multiplied by the UUID’s internal VLESS connections. Upload and download each receive their own configured maximum.

## Subscription intake

```text
App GET /sub/{sub_token} + x-hwid
  ├─ valid existing HWID → existing device UUID → subscription output
  ├─ valid new HWID within registration cap → atomically create device UUID → output
  ├─ valid new HWID over registration cap → reject with an actionable response
  ├─ absent/invalid HWID → existing compatibility path only; never silently create a new UUID
  └─ inactive/expired/over-quota account → existing subscription rejection
```

### HWID validation and storage

- Accept only `^[A-Za-z0-9=-]{10,64}$`.
- Store a normalized deterministic representation for equality and a salted one-way fingerprint for display/search privacy. Do not log the raw header.
- HWID is client-supplied and can be reset or forged. It identifies an installation for account policy; it is not hardware attestation or proof of one physical device.
- Add a per-account registered-HWID cap, separate from concurrent online slots. The default and administrator override need a product decision before implementation. A practical initial default is 10 registered HWIDs.
- Add last subscription refresh/use timestamps and an expiration policy for inactive HWID registrations. Pruning must never delete an online UUID or a device with current traffic/accounting records.
- Race-safe registration must lock the account and unique HWID identity. Simultaneous refreshes from the same new HWID must create one UUID; different new HWIDs must not exceed the registration cap.

### Compatibility

- Keep `/sub/{sub_token}/{device_id}` temporarily for applications that cannot send `x-hwid`; it continues to use the existing UUID.
- Do not make a missing HWID silently select an arbitrary existing device.
- A future strict-HWID setting may reject missing/invalid headers and set response headers explaining missing support or registration-cap rejection. Do not make strict mode the default until supported client coverage is established.
- The user portal should present one primary account subscription URL, explain supported HWID clients, show registered logical devices, and retain legacy per-device links only as a compatibility fallback during migration.

## Online slot accounting

```text
Xray statsUserOnline
  → node agent reports online UUIDs
  → Redis stores fresh node reports
  → account scheduler unions UUIDs over all fresh Exit nodes
  → one UUID = one slot regardless of connection count or Exit count
```

- Use the existing `OnlineTracker.CountOnlineForUser` UUID-union seam, not `CountDistinctIPsForUser`. A new freshness-aware union method may replace the old helper if it preserves the same public meaning.
- The authoritative online window is 20 seconds of Xray user activity. Agent reporting and Redis expiry must be tightened so the controller cannot treat stale 30/60-second reports as current sessions.
- Report empty online sets explicitly and delete/replace the prior Redis node report immediately; do not wait for stale TTL expiry.
- Store `online_since` only on a confirmed offline → online transition. Keep it while a UUID remains in the account-wide union, not merely while one node reports it. A node switch or multi-Exit use must not reset a UUID’s session start.
- Preserve a per-UUID latest activity timestamp and reporting node set for operator diagnostics, but do not use source IP to count concurrency.
- Entry L4 DNAT/masquerade makes exit-visible client source IP unsuitable for concurrency enforcement. IP remains an optional sharing/fraud signal only, never a concurrency slot key.

## Concurrency policy and enforcement

- **Do not enable forced eviction in production merely by setting `ENFORCE_CONCURRENCY=1`.** Guarded enforcement is implemented, but pending→all-Exit acknowledgment with complete online epochs still requires isolated DB/Redis and full distributed verification; observation is the safe rollout default until that gate is met.
- Evaluate every 10 seconds after reporting cadence supports the 20-second online window. Retain a configurable consecutive-observation threshold; initial recommendation: two confirmed sweeps.
- On confirmed excess, choose the UUID with the most recent **account-wide online transition** as the victim. Do not choose by IP count.
- Set `devices.evicted_until` for a short cooldown and immediately remove/revoke the UUID from all current Exit Xray inbounds when supported by the handler API. Verify in integration tests that existing Vision and XUDP sessions close; do not assume `RemoveUser` alone terminates every established flow.
- Central per-UUID permit authorization must reject evicted UUIDs immediately. This already protects controlled VLESS payload forwarding even before polling converges.
- A cooldown expiry permits a later subscription/config refresh and reauthentication; it must not automatically re-evict a stable, now-within-cap session.
- Keep the old IP-based sharing heuristic separate, disabled by default, and never let it evict a UUID solely because it appears behind an Entry relay address.

## Per-UUID speed policy

The newly implemented VLESS Reality device-egress path uses the issued UUID as its identity:

```text
Issued UUID
  → Xray authenticates UUID@proxima
  → exact UUID route to local authenticated SOCKS egress
  → central Redis bucket: UUID + upload/download direction
```

- All active VLESS connections under one UUID share one upload budget and one download budget.
- Different HWID-issued UUIDs receive independent budgets.
- A UUID using multiple Exits consumes the same account-wide budget per direction.
- This is forwarded-payload sustained-rate limiting with bounded bursts, not an instantaneous NIC wire-rate cap.
- Only VLESS Reality receives this guarantee in the initial release. Legacy non-VLESS compatibility paths must be withheld from limited plans after migration, not advertised with an unenforced speed.

## Data and interface changes

### Storage

- Add device HWID fingerprint/normalization, source metadata allowed by retention policy, first/last subscription refresh, last account-wide online transition, and retirement timestamp fields.
- Add a uniqueness constraint on account + normalized HWID fingerprint.
- Add indexes for account device lookup, active HWID-cap calculation, and online/retirement cleanup.
- Do not store the raw HWID in logs, activity payloads, browser responses, or general administrator list output.

### Public subscription interface

- Add `GET /sub/{sub_token}`.
- Read `x-hwid`; validate before device creation.
- Return controlled response headers for strict-HWID enabled, unsupported client, registration cap reached, and current assigned UUID state where safe. Never return UUID secrets beyond the normal subscription configuration.
- Preserve format negotiation and subscription-domain behavior from the current endpoint.

### Admin/user interface

- User portal: one primary URL, compatibility note, registered logical device list, last refresh/activity, revoke/forget action subject to online guard.
- Admin user detail: logical device count, online UUID count, concurrency cap, observation/enforcement state, most recent over-cap event and eviction reason.
- Plan editor: distinguish `registered device cap`, `online UUID concurrency cap`, and `per-UUID speed`. Do not call any of them simply “connections” without context.
- Node/route UI: retain device-egress capability/apply status. It is not proof a particular subscription is currently publishable.

## Required verification

### Subscription identity

- Same valid HWID repeatedly returns the same UUID.
- Concurrent first requests for one HWID create one device row.
- Concurrent distinct HWIDs cannot exceed the registration cap.
- Invalid/missing HWID creates no row; legacy endpoint continues to work.
- Raw HWID is absent from logs, activity output and error responses.

### Online UUID slots

- One UUID with many Vision TCP connections counts once.
- One UUID on two Exits counts once.
- Two UUIDs count twice, regardless of shared NAT/source IP.
- Empty agent report clears stale online UUIDs within the defined window.
- A node switch does not reset account-wide online transition time.
- An Entry-relayed session is counted correctly without depending on client source IP.

### Over-cap behavior

- In observation mode, no UUID is evicted and a structured observation is recorded.
- Under enforcement, the newest account-wide online UUID is selected after the configured confirmation threshold.
- Existing Vision and XUDP sessions for the selected UUID demonstrably stop; other UUIDs continue.
- Central permit requests for an evicted UUID fail immediately.
- A legacy IP sharing signal cannot evict a user merely because relay masquerade makes devices share the Entry IP.

### Speed and compatibility

- Two issued UUIDs with the same plan have independent directional speed budgets.
- Multiple connections and multiple Exits for one UUID share one direction budget.
- Limited-plan VLESS relay and direct endpoints are published only after compatible current-agent acknowledgment.
- Legacy non-VLESS endpoints are not advertised as speed-limited.
- Current supported Xray/agent interoperability, fail-closed Redis/API behavior and rate-change behavior continue to pass.

## Explicit non-goals

- Treating every TCP or UDP flow as an account concurrency slot.
- Physical hardware attestation from a client-controlled HWID.
- Restoring client source IP through nftables L4 relay or introducing PROXY protocol in this phase.
- Automatic load balancing/failover.
- Enabling strict HWID enforcement globally before compatibility and observation evidence exists.
- Claiming immediate hard-wire throughput, zero-downtime policy change or universal client support.

## Code impact anchors

- `api-server/internal/handlers/subscription.go`
- `api-server/internal/handlers/user_device.go`
- `api-server/internal/database/schema.go`
- `api-server/internal/services/online_tracker.go`
- `api-server/internal/scheduler/concurrency.go`
- `api-server/internal/handlers/node_agent.go`
- `node-agent/internal/stats/collector.go`
- `node-agent/internal/xray/grpc.go`
- `api-server/internal/services/xray_config.go`
- `node-agent/internal/deviceegress/`
- `web/src/pages/user/Devices.tsx`
- `web/src/pages/admin/Plans.tsx`
- `web/src/pages/admin/UserDetail.tsx`
