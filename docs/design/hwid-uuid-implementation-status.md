# HWID/UUID concurrency implementation checkpoint

Graph: `PLN-1790874094-3qa` (draft); Phase 1 `PHS-1790874094-cgs` is active. The plan is deliberately draft because this spec-graph validator requires unrelated active legacy requirements to be covered by any active plan; falsely mapping this HWID effort to them is prohibited. Generated IDs were created by the isolated v0.4.0-beta.8 CLI, not invented by hand. Phase exit remains open pending complete evidence.

## Implemented and verified increments

- New account URL `GET /sub/{sub_token}` validates x-hwid, stores only a per-account keyed fingerprint, atomically reuses/creates one device UUID, uses a separate 10-registered-HWID default cap and retains `/sub/{sub_token}/{device_id}` compatibility.
- User profile exposes the account subscription token; portal presents one primary URL and still exposes per-device fallback links for unsupported clients.
- HWID device removal is a tombstone rather than a deletion: same HWID cannot silently register again; retired devices are excluded from both subscription routes and the visible device list.
- Agent online UUID reporting and fresh Exit union now use a 10-second poll and the Xray 20-second activity window. Empty reports clear old UUIDs; Redis errors/missing reports are distinct from explicit empty reports; a cross-Exit UUID is counted once.
- Concurrency scheduler observes UUID slots, not IPs, selecting the newest account-wide online transition for over-cap reporting. Despite `ENFORCE_CONCURRENCY=1`, enforcement remains disabled until existing Vision/XUDP connections can be terminated with evidence.
- Per-UUID bandwidth budgets previously implemented continue to apply to issued UUIDs; this work does not change payload-rate semantics.
- Focused DB/Redis tests passed for HWID registration/concurrency/legacy route/expiry/token rotation, online UUID accounting and empty node reports. Web build, locale parity and a mock browser check of primary/fallback URLs passed. No live data was mutated.

## Outstanding acceptance gates

- Implemented and isolated-tested egress session termination: real Xray 26.3.27 Vision TCP/XUDP traffic stopped for revoked UUID A while UUID B continued. See `tests/evidence/device-bandwidth/uuid-concurrency-revocation.md`. The guarded scheduler's targeted DB/Redis epoch and acknowledgment tests also passed; a full deployed two-Exit integration and rollout remain.
- Guarded `ENFORCE_CONCURRENCY=1` writes pending UUID eviction epochs only on complete unambiguous observations with all Exits fresh and capable. Agents acknowledge closure per Exit; confirmed cooldown begins only after every acknowledgment. A new combined scheduler/API two-Exit DB/Redis test and three focused regressions passed without skips. Real multi-process Exit deployment and control-plane load remain unverified; no production rollout occurred. See `uuid-concurrency-enforcement-status.md`.
- Decide a guarded retention interval and implement inactive HWID retirement without deleting online or accounting-bearing records. Tombstones must survive to prevent re-registration.
- Validate end-to-end supported apps actually send x-hwid; absent header currently returns 400 on the new route and the legacy route remains the compatibility alternative. Do not claim all apps can use the primary URL.
- Browser tests for the full import/refresh flow, plan wording in every language and observation state; one mock URL render check is not evidence of a real client sending headers.
- Staged deployment: upgrade node agents before new API config, keep enforcement off until observer coverage and active-session termination are demonstrated.

No production rollout was established at this checkpoint. Subsequent implementation work was committed and pushed to main; see the later status/evidence documents for the current verification boundaries.
