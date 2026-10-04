# UUID concurrency enforcement verification update

## Established-session termination

An isolated Xray 26.3.27 test established active Vision TCP and XUDP traffic for device A alongside device B. Invoking the production node-local egress `ReconcileRevokedUUIDs([A])` stopped A's existing TCP stream and all three tested XUDP echoes, prevented a new A connection, and left B connected. Clearing a local snapshot while the central authority still denied A did not grant a bypass. Sanitized evidence: `tests/evidence/device-bandwidth/revocation-sanitized.json`.

By contrast, an isolated `RemoveUser` test showed existing Vision TCP forwarding 245,760 more bytes and existing XUDP echoing 3/3 datagrams after Xray user removal. The node-local egress closure, not `RemoveUser`, supplies active-session termination.

The agent polls a node-authenticated full revocation snapshot and closes idle sessions if the snapshot becomes older than two seconds, independently of a stalled fetch. On snapshot/API failure it fails closed; it does not treat an error as an empty revoked set. The backend snapshot includes eviction, retirement and inactive/expired/exhausted account state across Exits, including those no longer in a plan's current group. Deleted legacy devices now retain retirement tombstones.

## Distributed enforcement remains intentionally disabled

The local closure primitive is verified, but `ENFORCE_CONCURRENCY=1` is still ignored. A distributed safety review found that scheduler-time ties cannot prove which UUID joined last, stale/unknown Exits can falsely reset session transitions, evictions are logged before fleet-wide acknowledgments, and a two-second refresh interval with a two-second snapshot watchdog can disconnect healthy users on routine latency. Do not switch on the dormant scheduler branch on the strength of local closure tests alone. Require an ambiguity-safe victim policy, fresh Exit capability coverage, pending/confirmed eviction acknowledgments, and a measured watchdog margin before enabling.

## Remaining rollout gate

The scheduler is still observe-only even if `ENFORCE_CONCURRENCY=1`. A safe full enforcement release needs an authenticated end-to-end test of: scheduler selecting the newest online UUID, DB eviction write, multi-Exit snapshot dissemination, both Exits closing established Vision/XUDP flows within the expiry bound, and unaffected devices remaining live. A standalone egress revocation test does not prove the entire distributed chain. Missing telemetry must not falsely count as zero or reset account-wide online transition. Operators must observe real UUID distributions before enabling enforcement.

No deployment or live subscriber traffic was involved in these tests.
