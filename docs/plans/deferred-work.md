# Deferred work: backlog and decisions needed

Status: Planned. Nothing in this file has been started. Written 2026-10-16 after
the client-address cap work (PRs #2 to #7) landed on `main`.

Each item says what it is, why it was deferred, and what has to be decided or
provided before it can start. Items are grouped by who has to act.

## A. Decisions needed from the owner before work can start

### A1. mihomo as the server-side core

Decided: deferred. Xray-core stays the Exit core. Branch `feat/mihomo-core` is
kept and equals `main`; no code is written on it.

Verified so far (against mihomo v1.19.32 source, not a running system):

- mihomo can serve VLESS+REALITY+Vision, Hysteria2, TUIC v5, Trojan, Shadowsocks
  and Mieru, and exposes `/connections`, `DELETE /connections/{id}` and
  `/traffic`.
- The path that avoids a private fork is to import mihomo as a Go library, pin the
  tag in `go.mod`, wrap `C.Tunnel` for connect-time checks, and read
  `statistic.DefaultManager` for per-user bytes and online IPs. `C.Tunnel` is a
  three-method interface and `statistic` is public.
- When mihomo is a dependency its `replace` directives are ignored, so the
  `replace google.golang.org/protobuf => github.com/metacubex/protobuf-go ...`
  line must be copied into our `go.mod` and re-checked on every bump.
- The dependency tree is large (136 direct requirements against 18 in
  `node-agent` today), which grows the binary and the build time.

Not verified: that the library route works at all. A prototype is the next step,
scoped to one VLESS+REALITY listener behind a `Tunnel` wrapper, checking that
`InUser` and the original source IP are visible. Do not start the migration
before that passes.

Decisions required:

1. **How users are changed without dropping connections.** Stock mihomo has no
   public handle to update a listener's user set, because the service field is
   unexported. Two workarounds exist: recreate that listener when its users
   change (the live connections of that listener drop), or put every candidate
   user in the listener and reject non-allowed ones in the `Tunnel` wrapper
   (no drops, but revoked UUIDs stay in the listener config). The second is the
   closer match to today's zero-disruption behaviour and carries a security
   trade-off that needs an explicit design.
2. **Process model.** A library core runs in the node-agent process, so a core
   crash takes the agent with it. Today Xray is a child process.

Hard constraints that carry over unchanged: one subscription URL and one shared
account UUID per account; never a per-device UUID or link; never revoke the
shared UUID to enforce a limit.

### A2. Connect-time refusal of an over-cap client

Today enforcement is after the fact: the scheduler sweeps every 10 s, evaluates
an account every 30 s, and acts after 2 consecutive over-cap sweeps, so a new
over-cap client is served for roughly 60 to 80 s (measured 66 to 77 s in the
end-to-end test) before its tunnel is closed.

Deferred to the core-modification work, because it needs a hook between
authentication and dispatch that stock Xray does not offer. Mihomo's `Tunnel`
wrapper (A1) is the natural place; for Xray it needs a maintained core patch.

Open problem to solve first: a source-qualified allowlist denies an unknown IP
at routing time, before any admission call, so a legitimate new device could
never be admitted. The design in `docs/design/tunnel-session-concurrency.md`
describes it; it has no answer yet.

## B. Checks only the owner can do

### B1. Review plans before relying on default-on enforcement

The cap is `COALESCE(max_concurrent, max_devices, 0)`. A plan that sets only
`max_devices` is now a live concurrent-IP cap. The end-to-end scripts create
plans with `max_devices` 1 and 3. Review every active plan.

### B2. Confirm each Exit can close connections

Closing needs `CONFIG_INET_DIAG_DESTROY` and `CAP_NET_ADMIN`. The agent probes
this at startup and logs either `client address limit: connection closing is
available on this node` or a `WARNING` naming what is missing. Check that line on
every Exit after deploying. Checked so far: Ubuntu 24.04 and 26.04 kernels have
the option; Alpine `lts`/`virt` configs set it; Docker Desktop's kernel does not;
Debian was not checked.

### B3. Decide the repository-hygiene item DEP-10

A README passage describing an unrelated script that fetched a chatgpt.com
session token entered commit `1c0d733`, now in `main` history. The script file
and the README text are gone from the current tree. Whether to rewrite history
(force push) is the owner's call and is not done.

## C. Work that can be done, not yet requested

### C1. Extend the cap end-to-end test beyond one Exit, direct, IPv4

`tests/direct-e2e` now proves the newer address is cut off and the older one
kept, but only for one Exit, a direct connection and IPv4. Not exercised:

- **Entry relay**: the original client IP must come from the authenticated Entry
  proof, never the Entry address. `tests/relay-e2e` observes this but does not
  assert closing.
- **Two Exits**: the cap is summed across Exits and a closure is queued per
  Exit. DEP-05 in `docs/design/deployment-test-issues.md` still lists this as
  unverified.
- **IPv6**: the /64 slot key is unit-tested and tested against Redis, but no
  tunnel from an IPv6 client has been run.

### C2. Measure the "abandoned address" threshold

The 120 s staleness threshold that makes a dead Wi-Fi address lose to a live LTE
address is an estimate. It has not been checked against real mobile clients.

### C3. Measure permit cost under load (DEP-06)

The per-payload central permit traffic capacity is not established. Measure
admission and permit latency, API request and error rates, Redis latency and DB
connections under bounded controlled load before enabling the shared budget
fleet-wide.

### C4. Pin the Xray version

`scripts/install.sh`, `admin_node.go` and `StageBinary` all use
`releases/latest`. It resolves to a stable release today (v26.3.27), so nothing
is broken, but a future release is installed untested. Pin to a verified version
and move it deliberately. Also compare the installer floor (`v25.1.1`) with the
end-to-end image (`v26.7.28`), which are not the same.

### C5. Known small defects

- `uuid_slot_reconcile.go` scans `last_seen>NOW()-INTERVAL '20 seconds'` into a
  `bool`; for a node that has never sent a heartbeat `last_seen` is NULL and the
  scan fails, logging `UUID slot reconciliation unavailable`. Other call sites
  already wrap this in `COALESCE(..., false)`. Seen in the end-to-end logs.
  Behaviour impact is not assessed.
- `tests/direct-e2e/cap.sh` waits on the scheduler's real intervals (about 75 s).
  A faster run needs the intervals to be configurable by environment, which they
  are not.

## Recorded, not planned

`archive/backup-9commits-2026-09-16` is a local tag only (not pushed). It
preserves the old `backup-9commits` branch, whose unique content was checked
and found to already be in `main` in a newer form. Delete the tag when no longer
wanted.
