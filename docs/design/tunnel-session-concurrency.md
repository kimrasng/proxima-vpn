# Concurrent client-address limit and tunnel sessions

## Current contract: concurrent client source IPs per account

Superseding the earlier "count sessions, not addresses" wording (user decision):
a plan cap `N` (`COALESCE(max_concurrent, max_devices, 0)`, explicit zero is
unlimited) means **at most N distinct client source IPs may be connected at
the same time per account, summed across every Exit**. A device is
deliberately defined as one source IP. Two devices behind the same NAT count
as one; no false-positive handling or HWID heuristics are added for that.

Hard constraints from the product:

- There is exactly **one subscription URL and one shared account VLESS UUID**
  per account. No per-device UUID or per-device subscription link may be
  introduced, and the limit must never be implemented by revoking the shared
  UUID, which would disconnect every device.
- `x-hwid` is not an admission input. It is a subscription-fetch header only
  some clients send; it is not carried into the VLESS handshake.
- The original client IP behind an Entry relay (which masquerades) must come
  from the authenticated Entry original-source proof, never from the Entry
  address and never from a client-supplied value.

What exists today (verified in code, not in a live environment):

- Observation: `ObserveClientAddresses` joins fresh Exit tunnel reports with
  fresh Entry flow reports into a set of client IPs with first-seen times.
- Bookkeeping: the scheduler runs `ObserveAndReconcileClientAddressSlots` per
  active account (also for unlimited plans) at most every 30s. It is
  **release-only** and logs a counts-only report when a complete observation
  exceeds the cap for the configured number of consecutive sweeps. It never
  revokes a UUID and never closes a connection.
- Admission primitives: `ReserveObservedClientAddress` (atomic, account-wide
  Redis slots), `AdmitSourceDevice`, `AdmitProvenRelaySource`.
- Exact transport close: `QueueTransportClosure` and the node-agent
  `CloseObservedTransport` close one fenced Exit transport without touching
  the UUID.

What is **not** active, so the limit is currently not enforced anywhere:

- `WithClientAddressAllowlist` (source-qualified Xray routing) has no
  non-test caller, so the API never emits source-qualified rules. The
  node-agent `SetSourceAdmitter` hook is wired but only fires for credentials
  that carry a source address, which only such rules create.
- `AdmitRelayPeer` in the node-agent client has no non-test caller.
- Nothing releases an IP slot on tunnel close; release relies on the fenced,
  receipt-timed absence reconciliation above.
- A source-qualified allowlist denies an unknown IP at routing time, before
  any admission call, so a legitimate new IP could never be admitted without
  a dynamic rule update. This first-contact problem is unresolved; do not
  enable the allowlist until it has a designed answer.
- IP slots are keyed by the exact address string. IPv6 privacy-address
  rotation and a Wi-Fi/LTE switch can therefore make one device hold two
  slots. IPv6 /64 normalization and a switch grace period are required before
  enabling enforcement.

Automatic enforcement stays OFF. Database and Redis integration tests have
not been run in the current environment.

## Earlier session-transport model (not the product contract)

The text below describes the earlier transport-count work. A session is one
authenticated live inbound tunnel transport owned by an Exit. Separate
transports sharing one account UUID and source address consume separate
slots. Several destination streams inside a single tunnel do not. Entry NAT
attribution may help locate a connection but does not change its slot
identity; an Entry hop is not an additional session.

One installation can create several counted transports with the generated
TCP/REALITY/Vision profiles. A session limit must not be advertised as a
physical-device limit. See [Xray research and implementation recommendation](xray-session-limiter-research.md)
for upstream evidence and the proposed authentication-time admission path;
that proposal is not an enabled or approved runtime change.

The source-address cohort primitives are now the basis of the current
client-IP contract above, but they remain disabled. They must still not be
used to deduplicate transport-session slots.

Implemented initial policy tests verify:
- same IP and UUID with different TCP peer ports count separately;
- repeated reports of one exact Exit transport count once;
- transports at different Exits count separately;
- incomplete snapshots, conflicting ownership and tied cutoff times do not
  provide permission to evict an arbitrary session.

The control-plane admission handler validates a submitted token against its fresh authenticated Exit report and performs an account-wide, plan-locked Redis slot reservation. Its HTTP contract returns 409 for a verified N+1 session and 503 for unknown/stale evidence. Session tokens are shared across packages and bind Exit ID, canonical credential UUID, local/peer endpoints, and acceptance time. The concurrency scheduler runs receipt-aware reservation reconciliation for active accounts (including unlimited accounts) at most every 30 seconds; it only releases slots after stable complete fleet reports and separated server receipt timestamps. Redis/DB integration cases remain unexecuted in the current environment. Live topology and Xray rule validation also enforce matching device identity, exact source/port, local SOCKS endpoint, and reject IPv4-mapped source ambiguity.

This reconciliation is bookkeeping cleanup, not transport termination: if a TCP tunnel has already closed, Xray/the OS has closed that socket; the scheduler only removes its stale Redis slot after complete reports confirm the transport absent twice with separated receipt times. It never closes a still-live tunnel.

This is still **not an enabled runtime path**: the node-agent does not create/apply the per-transport egress credential from deny-first observation or call session admission before SOCKS success, and no verified actual-Xray-transport-close callback releases that exact slot. The existing local SOCKS callbacks follow SOCKS association lifetimes, which are not proven equivalent to inbound tunnel lifetimes. A new TCP retry may also use a different source port, so the current source+port session rule cannot be assumed to admit client retry. Supported-client first-denial retry and direct/relay N+1 end-to-end tests remain outstanding. Keep automatic enforcement disabled.

The read-only admin endpoint displays the configured plan session limit and
warns if a complete observation exceeds it; it explicitly says no automatic
block has been confirmed. An incomplete observation never shows a numeric
count or over-limit judgment. Browser tests cover 2 sessions alongside one
observed IP, unknown reports and the warning.

Default runtime still uses the legacy UUID admission path and must not be
advertised as a session-count limiter.
