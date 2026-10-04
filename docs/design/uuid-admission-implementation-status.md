# UUID admission-before-forwarding implementation checkpoint

The requested rule is one account slot per newly online Xray UUID, not per TCP/UDP connection. Existing admitted UUID sessions keep their slot; a new UUID is denied at a full plan cap without disconnecting incumbents.

## Implemented increment

- A separate node-authenticated `/devices/admit` request checks the current device/user/plan entitlement, then reserves an account-wide Redis set membership atomically across Exit nodes. Capacity rejection is terminal; 0 cap retains the existing unlimited convention.
- The node-local authenticated SOCKS egress invokes central admission **before reporting success to Xray for TCP CONNECT or UDP ASSOCIATE**. Bandwidth remains per UUID/direction on later payload permits. Xray VLESS/Reality handshake can still succeed before the SOCKS egress rejects this attempted destination; this does not claim pre-auth rejection.
- An agent reports UUIDs with successful admission and still-open associations every ten seconds. An idle association remains reported; failed admission is not reported. A generation/sequence fence rejects stale process reports and missing reports are unknown rather than empty.
- Account membership has no TTL. On a complete fleet of fresh Exit association reports, an absent UUID becomes a release candidate; it is returned only after a second complete observation at least 30 seconds later. A reservation nonce and Redis server timestamps prevent an older pre-reservation report from releasing a new member. The reconciler runs every ten seconds and returns slots conservatively; incomplete/failing Exit reports preserve incumbents and deny newcomers rather than inventing available slots.

## Verification and limitations

- Targeted Go tests for pre-success TCP/UDP admission denial, incumbent continuity, terminal capacity response, reporter association lifetime, and revocation passed.
- Fresh isolated PostgreSQL17/Redis7 tests for two simultaneous Exit reservations, pre-admission empty reports, idle incumbent preservation, incomplete Exit reports, and confirmed disconnect release passed without skips. The verifier task stalled after starting disposable test containers; the parent executed these targeted checks, removed the verifier's two containers, and left unrelated pre-existing Docker resources untouched.
- No deployed two-Exit end-to-end admission test or production load measurement was performed.
- A full account may remain full when Exit telemetry is missing, the agent is offline, or a newly reserved UUID fails before an association opens. This is intentional fail-closed availability; administrators need an audited recovery path instead of silent timeout-based eviction.
- An old agent that does not call `/devices/admit` cannot deliver this new contract. Upgrade agents before enabling this protocol in production.
