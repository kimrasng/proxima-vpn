# Server-observed concurrent client limits

Status: PHS4 implementation in progress. **Enforcement of multiple clients
sharing an account UUID is not implemented yet.** The existing UUID admission
and eviction paths count credentials, not physical devices.

## Identity contract

The account subscription handler ignores `x-hwid` entirely, including valid,
invalid and changing values. It neither hashes nor persists that header, nor
uses it as a correlation hint. The account URL reuses the oldest live account
credential (or creates one on first fetch). Explicit legacy device URLs remain
compatible. Historical HWID database columns remain for non-destructive
migration, but have no runtime consumer.

Without trustworthy client-side identity, an IP-based observer cannot count
physical devices exactly. Multiple devices behind one NAT share one public
address; one device switching networks can temporarily appear under two
addresses. The intended operational signal is **distinct observed client
addresses per account**, not an assertion of distinct physical devices.
Multiple TCP connections and multiple Exits for the same address count once.
Client-controlled identity headers do not improve or alter that signal.

## Entry observation implemented so far

For TCP forwarding rules, the Entry declares an nftables `client_flows` timed
set. A non-verdict postrouting rule runs after masquerading and records:

```
(peer IP, peer port, Exit IP, Exit port, original client IP)
```

Here peer IP and port are the source tuple the Exit sees after NAT. The final
field is conntrack's original source address. Each original-direction packet
refreshes the entry's five-minute timeout. The set is bounded to 262144 keys.
It does not change DNAT, masquerading, verdicts or per-route traffic counters.
The existing policy replacement recreates the set; consumers must treat the
resulting observation gap as unknown, not proof that a client disconnected.

`relay.Manager.Flows()` reads the set without consuming or resetting it. A
missing set, uninstalled policy, read failure or malformed tuple is an error.
A policy with no TCP forwarding routes produces an explicit empty result.

Tuple reuse is deliberately preserved: two original source addresses may
appear for the same NAT tuple during the timeout. A server must not guess
which owns an Exit connection. Enforcement must wait for unambiguous evidence.
An expired Entry tuple must similarly not be interpreted as a direct client.

Initial supported observation path: VLESS Reality over TCP, including UDP
payload carried inside that tunnel. Native UDP protocols are not covered.

## Exit observation implemented so far

The agent indexes authenticated TCP peers from Xray access output and joins
those peers against established Linux TCP sockets read via NETLINK_SOCK_DIAG.
TCP_INFO supplies the last-data-received age. Missing access records,
conflicting UUID ownership, index overflow, socket-dump errors and process
restarts make the observation unknown. Log records alone never establish
liveness. A new Xray process resets the ownership index.

Entry and Exit snapshots are carried by authenticated node heartbeats and
stored with a 25-second Redis TTL. Unknown or malformed reports invalidate
previous evidence. The API has an observational account-wide join that
unions client addresses across credentials and Exits; it does not invoke
UUID-wide eviction, which would disconnect every client sharing that UUID.

The Linux socket observer passed a live TCP connection/close test in a Debian
container with all capabilities dropped. Selective enforcement remains open.

## Remaining integration

- Actual Xray relay E2E passed 27 assertions, including authenticated Exit
  transports joined to the original Entry-side client address. The observer
  endpoint returns an unknown count for incomplete reports. The admin UI
  separately shows observed addresses, not physical-device claims.
- Exact TCP socket-close support is implemented using Linux SOCK_DESTROY with
  an authenticated-owner/start-time fence, but the current Docker kernel
  returns EOPNOTSUPP. Successful selective closure and survivor isolation are
  **not verified**. No automatic address-limit scheduler is enabled.
- Preventing immediate reconnection needs an account/address-specific deny
  mechanism as well as connection closure; closure alone is not enforcement.
- Restart/tuple-reuse lifetime handling and bounded ownership-index cleanup.
- A tested no-SOCK_DESTROY building block now allows several server-generated
  local passwords for one UUID. Removing one password closes only its TCP
  egress sessions; retained sessions and UUID-wide bandwidth gates survive.
- Xray 26.7.28 source-qualified routing was verified live: exact IPv4 sources
  select different outbounds, a gRPC rule swap preserves an existing survivor.
  IPv6 live routing remains unverified.
- Canonical per-source outbound materialization uses stable passwords per tag,
  exact host CIDRs and a required unconditional blackhole fallback. Shared
  catchall bypasses and broad networks are rejected.
- Address-limit selection proposes the newest address only from complete,
  unambiguous evidence. Account-wide starts survive Exit switches and partial
  reports; confirmed absence resets them. The administrator endpoint exposes
  the proposal with `enforced: false`.
- The real-Xray selective egress test now covers removing one source and the
  final source: targeted existing streams close, removed sources cannot
  reconnect, survivors continue, and the same Xray PID remains running.
- API canonical address allowlists retain all controlled-inbound deny
  fallbacks and emit explicit credential deny when the list becomes empty.
  This builder is still not automatically applied to production config.
- Atomic account-wide observed-address reservations prevent concurrent
  Exits from admitting N+1 addresses. Reservations deliberately have no TTL:
  lost observations are not evidence that live connections closed. Confirmed
  absence/release and production admission integration remain open.
- Address-slot release now has a tested admission-sequence fence, a minimum
  30-second absence interval, replay protection, and incomplete-report reset.
  Collection captures the reservation fence before reading fleet reports.
  Independently published Entry/Exit snapshots now have server-generated IDs;
  a stable fleet fingerprint fences release against reused reports. Legacy
  unidentified reports are unknown. Redis failover/persistence and automatic
  release integration still need a gate.
- A policy proposal retains older address cohorts up to the cap; equal start
  times at the cutoff suspend selection instead of choosing arbitrarily.
- These primitives are **not yet an integrated enforcement path**. Initial
  source admission, relayed source-to-egress association, automatic policy
  construction and fleet policy acknowledgement remain open.
- Relay route proposals now associate an original source-address cohort with
  the exact masqueraded peer IP/port; missing or ambiguous tuple joins fail.
  Xray protobuf encoding supports exact source-port ranges (unit tested).
  These proposals cannot migrate already-dispatched shared egress sessions
  and are not automatically applied; initial relay admission is still open.
- Same-account multi-address direct/relayed enforcement E2E remains open.

Do not enable or advertise these unfinished paths as enforcing device limits.

## Verification

- Account-subscription HTTP tests verify changing `x-hwid` values produce no
  additional credential and do not change the existing UUID.
- Relay unit tests cover explicit empty versus unknown reports, malformed
  data, duplicate keys and ambiguous tuple reuse.
- `NFT_CHECK=1 go test -run TestRenderNftablesSyntax ./internal/relay` validates
  the generated TCP/UDP/combined-role rules against real nftables in Linux.
- Disposable network-namespace experiments verified post-NAT/original-source
  tuple observation with nftables on Debian 12, Ubuntu 22.04, Rocky 8 and
  Alpine 3.20. These experiments are not evidence of completed enforcement.
