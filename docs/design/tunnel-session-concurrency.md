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
  active account (also for unlimited plans) at most every 30s. Reconciliation
  itself is release-only: it frees stale slots and never revokes a UUID. When a
  complete observation exceeds the cap for the configured number of
  consecutive sweeps, the scheduler closes the exact transports of the
  over-cap addresses (on by default, see "Enforcement is on by default").
- Admission primitives: `ReserveObservedClientAddress` (atomic, account-wide
  Redis slots), `AdmitSourceDevice`, `AdmitProvenRelaySource`.
- Exact transport close: `QueueTransportClosure` and the node-agent
  `CloseObservedTransport` close one fenced Exit transport without touching
  the UUID.

What is **not** active, so the limit cannot yet be applied at connect time:

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
- Wi-Fi/LTE switching is only partly handled: see "Address identity and
  network switches" below. The first-contact problem above remains the blocker
  for connect-time enforcement.

### Enforcement is on by default

Closing the connections of over-cap client addresses is **on by default**.
`ENFORCE_CLIENT_ADDRESS_LIMIT` is now a kill switch: set it to `0`, `false`,
`off` or `no` to stop closing. Any other value, including a typo such as
`flase`, leaves enforcement on, so a mistyped override cannot silently disable
the limit. It is independent of `ENFORCE_CONCURRENCY` and never revokes the
shared account UUID; only the exact transports of the denied addresses are
closed.

What this is, and is not:

- **After the fact, not at connect time.** The scheduler sweeps every 30 s and
  acts after the cap has been exceeded on two consecutive complete sweeps, so a
  new over-cap client is served for roughly a minute before its connection is
  closed. It is not blocked at the door. Connect-time refusal needs the
  first-contact problem above solved, which needs an Xray/mihomo core patch.
  Those figures are derived from the configured intervals, not measured.
- **No ban.** A closed client may reconnect and is served again until the next
  sweeps catch it. Closures for one account are rate-limited to one per 90 s.
- **Acts only on complete evidence.** An incomplete observation, an ambiguous
  oldest/newest tie, or a missing start time closes nothing.
- **Plans that set only `max_devices`.** The cap is
  `COALESCE(max_concurrent, max_devices)`. A plan that has `max_devices` but no
  `max_concurrent` is now a live concurrent-IP cap. Review such plans before
  deploying.
- **Needs kernel support on each Exit.** Closing uses `SOCK_DESTROY`, which
  needs `CONFIG_INET_DIAG_DESTROY` and `CAP_NET_ADMIN`. The generated systemd
  unit sets no `User=`, so the agent runs as root and has the capability. The
  agent probes this at startup and logs a `WARNING` naming the missing piece,
  so an unsupported host is visible instead of silently never closing.
  Alpine's `lts` and `virt` kernel configs set the option to `y`; Ubuntu kernels
  are reported to as well. Debian's was not checked. Docker Desktop's linuxkit
  kernel does not have it.

Not verified: no real connection has been closed by this path in this
environment, because the only Linux kernel available here (Docker Desktop,
linuxkit) lacks the option. The probe's "capable" answer relies on the kernel
returning `ENOENT` for a tuple that matches nothing; that is read from the
kernel source, not observed. Run `TRANSPORT_CLOSE_LIVE=1 go test -run
'TestCloseTransportPeerLive|TestProbeTransportCloseLive' ./internal/xray` as
root on a real Exit before relying on it.

### Address identity and network switches

Two behaviours that previously made one device look like two clients are now
handled, and both are covered by tests (the Redis-backed ones against a real
Redis 7):

- **One slot per IPv6 /64.** `NormalizeClientAddress` is the single slot-key
  definition used by observation, admission and release. IPv4 and IPv4-mapped
  IPv6 keep the exact address; IPv6 collapses to its /64 network address, so
  SLAAC privacy-address rotation does not consume a second slot. Release
  uses the same key, otherwise a live /64 would never match the slot admission
  created. Closing a denied /64 closes every transport observed inside it.
  Consequence: hosts on one /64 count as one client, as devices behind one
  IPv4 NAT already do.
- **Abandoned addresses are denied first.** The Exit reports, per transport,
  the kernel's time since the peer last acknowledged anything
  (`tcpi_last_ack_recv`). An address whose freshest transport has been silent
  for 120 s or more is treated as abandoned and sorts after every active
  address, regardless of age. A phone that moved from Wi-Fi to LTE therefore
  loses its dead old address rather than its live new one. The admin
  candidate (`DecideClientAddressLimit`) and the enforcement policy share this
  ordering. Without idle evidence the previous newest-denied order is used.

This is an ordering preference, not a grace timer: nothing waits for the old
address to expire before the new one is judged. Enforcement still acts only
after the configured number of consecutive over-cap sweeps and respects the
per-account closure cooldown. The 120 s threshold is an estimate, not a
measured value; it has not been validated on real mobile clients. An Entry
that terminated TCP itself (a proxy rather than the current NAT-forwarding
relay) would acknowledge on the client's behalf and keep every Idle near zero,
which degrades safely to the newest-denied order.

Verification so far: unit and Redis-backed tests, `go test -race` across
the three Go modules, golangci-lint v2.13.1, and the TCP_INFO offset checked
against `golang.org/x/sys/unix.TCPInfo` and against a live socket. The
previously reported `TestUUIDSlotReconciliationReleasesDisconnectedAfterTwoFreshEpochs`
flake is unrelated and still occurs on the unmodified base. No test has run
with real clients or a deployed two-Exit topology.

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
