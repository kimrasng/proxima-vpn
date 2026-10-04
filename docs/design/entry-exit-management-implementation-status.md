# Entry-centered management implementation status

This records an implementation increment against `entry-exit-plan-management-approved-spec.md`; it does not mark the entire approved specification complete.

## Implemented increment

- Fresh relay installations register first and read the authoritative role before VPN setup. Relay installs skip Xray and unzip; exit/combined installs retain Xray setup.
- All/Entry/Exit inventory views include combined roles and use URL-backed role selection.
- Group assignment is separate from server availability and does not disappear because a route is disabled.
- Entry port maps include legacy and direct routes and expose navigation from server inventory/details.
- Multi-Exit creation previews exact port assignments before atomic backend creation. The current chain schema still requires registered endpoints and a managed Entry hostname; it does not yet persist pre-registration or hostname-less route drafts.
- Saved-plan route assignment has its own panel, preserves unrelated draft edits and isolates changes when groups are shared.
- Configuration checks, enabled state and lack of policy acknowledgments are explicitly distinct.
- Shared-tier shaping and unsupported per-device caps are explicitly called out.

## Update: per-device bandwidth implementation

The previously missing device limiter now has a centralized Redis permit API, authenticated node-local SOCKS egress, deterministic Xray identity routing, runtime password materialization, and limited-subscription capability gates. See `per-device-bandwidth-contract.md` for the confirmed payload-rate contract, failure behavior and deployment constraints. This does not mark the other outstanding UI/readiness/metrics work complete.

## Not implemented / not advertised as complete

- Production deployment, fleet load/latency capacity and live-host throughput validation.
- Pre-registration or hostname-less route draft persistence.
- Authoritative subscriber-output preview shared with actual raw/Clash/Sing-box serialization.
- Per-route policy-generation acknowledgments and safe publication gate based on applied configuration.
- Per-route forwarding metrics and Entry capacity/congestion attribution.
- Same-port automatic load balancing or failover (out of scope).

## Historical limitation and chosen solution

At the earlier UI increment, Xray grouped credentials by Mbps and Linux shaping capped shared tier ports. Independent per-Exit caps would multiply one device's budget. The new implementation replaces shared-tier enforcement for managed VLESS paths with an authenticated egress proxy and one central Redis budget per device/direction. Legacy tier endpoints remain only for client compatibility.

The approved per-device target has a code implementation using central budgets; deployment readiness remains contingent on the recorded verification gates and control-plane capacity.

## Verification evidence

- Web TypeScript/Vite build and locale parity passed.
- Synthetic browser checks cover inventory role views, combined-role navigation, multiple-Exit preview/commit, conflict input retention, direct routes, invalid URL filters and narrow layout. Existing DNS/SNI editor coverage also passed (19 checks); technical columns are enabled through Preferences for those checks.
- Saved-plan route browser checks passed, including preserving drafts on failure, isolated-group rebasing, route-only discard confirmation and external-group conflicts. Only affected scenarios were rerun after fixes.
- Stubbed installer checks: 15 passed, including real-curl cross-origin redirect rejection. No privileged installation on this host.
- Isolated PostgreSQL checks: 8 scenarios passed without skips across the initial six and two affected concurrency regressions. Temporary database/container cleaned up; existing databases untouched.
- Focused handler tests and server compilation passed. No live throughput, installed-host nftables, or deployed policy acknowledgment evidence is available.

## Operational cautions

- Port preview is not a reservation; commit may fail after a competing claim.
- New route assignments are configuration, not proof of reachable or usable subscriptions.
- Empty plan route assignment deliberately removes the plan's selected paths; changing shared groups must not change other plans.
- Live deployment, privileged host installation and live network throughput are not established by synthetic browser/installer checks.
- Existing workspace changes are extensive and predate this increment; no wholesale commit or cleanup should include unrelated work.
