# Guarded UUID concurrency enforcement increment

Status: implemented behind `ENFORCE_CONCURRENCY=1`, not deployed. Phase 2 remains open pending DB-backed integrated verification and operator rollout evidence.

## Enforcement path

1. Every account sweep requests a complete observation epoch across registered Exit/both nodes. Missing, stale, malformed, or errored reports make it incomplete; the scheduler does not evict on incomplete evidence.
2. Complete epochs union active UUIDs across Exits, preserve account-wide starts on node switches, and mark equal/unknown newest starts ambiguous. Ambiguous newest selection suspends enforcement rather than evicting a lexicographic guess.
3. If UUIDs exceed the plan cap in the configured consecutive sweeps, and *every* Exit reports fresh compatible device-egress status, one account-level transaction selects a target, writes a pending eviction epoch listing all required Exit IDs, and denies the UUID through a non-expiring `evicted_until` sentinel.
4. The fleetwide authenticated revocation snapshot reaches every Exit; the node-local egress closes all current TCP/UDP sessions belonging to the UUID before submitting an epoch acknowledgment. Errors fail closed. An independent watchdog expires a stale revocation snapshot after five seconds; polling refreshes every 500 ms.
5. The epoch is confirmed only when all required Exit IDs acknowledge. The cooldown is computed from `confirmed_at`, not from the initial eviction request. A missing acknowledgment never becomes success and leaves the UUID banned and the next eviction blocked. On cooldown expiry, the epoch is deleted by the scheduler sweep.

## Operator visibility added

The administrator can open `/admin/uuid-evictions` to inspect each pending or confirmed UUID eviction, its epoch, required Exit nodes and which nodes have acknowledged closure. A retry action preserves the ban and epoch; it never forges an acknowledgment or releases a still-connected UUID. A mocked Korean browser check exercised a partially acknowledged two-Exit row and the retry action.

## Evidence

- Real isolated Xray 26.3.27 tests show that `RemoveUser` alone leaves existing Vision TCP/XUDP connections alive, while node-local `ReconcileRevokedUUIDs` closes device A's established sessions and preserves device B. See `tests/evidence/device-bandwidth/remove-user-does-not-close.md` and `tests/evidence/device-bandwidth/revocation-sanitized.json`.
- Targeted Go tests for the service/handler/agent paths and spec-graph validation passed. Web build passed.
- A combined scheduler/Fiber two-Exit test now exercises complete online epochs, consecutive over-cap sweeps, the central permit denial, authenticated revocation snapshots on both Exits, pending state after one ACK, confirmation after two ACKs, and a cooldown starting only then. `TestUUIDEvictionEndToEnd` and three focused regressions all passed with no skips on isolated PostgreSQL 17/Redis 7. The first simultaneous multi-package run raced schema migrations; rerunning packages after migrations completed passed. This is not a full deployed two-Exit node-agent process test. The verifier removed only its test containers and images; a separate `proxima-vpn-*` stack appeared during verification and was left running, untouched.

## Safe limitations

- `ENFORCE_CONCURRENCY=1` now enables *guarded* enforcement, but will not evict if any registered Exit is offline/stale/incompatible, if Redis observation is incomplete, if the newest UUID is ambiguous, or if an earlier eviction is awaiting acknowledgment. An operator must resolve those blockers rather than interpreting silence as policy success.
- The scheduler and agent protocol passed isolated DB/Redis control-plane and separate real-Xray local egress tests, but have not yet been demonstrated together as a full deployed two-Exit process setup. Stage agents first and measure permit/control-plane capacity before enabling this flag on production.
- A pending eviction remains banned indefinitely when an Exit never confirms. The admin can now see missing acknowledgments and safely request retry, but explicit recovery of an unreachable/deleted Exit without forging an acknowledgment remains a future audited operation.
- HWID is client-provided and identifies a logical installation, not cryptographically attested hardware.
