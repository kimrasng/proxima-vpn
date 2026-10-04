# Approved specification: Entry-centered routing and plan availability management

## Approval and tracking

The user approved this proposal as the second improvement effort after minimal relay-only installation. Approval covers both administrator UX and backend improvements, not implementation or deployment.

The user explicitly chose **per-device maximum speed**, not account-wide speed sharing and not a shared speed-tier cap.

Implementation has not started. Graph plan registration is pending: installed spec-graph v0.3.4 requires explicit legacy IDs, while the current operator contract requires CLI-generated IDs. No IDs were invented and no graph entities were created for this specification. This is an approval/specification record, not a replacement for the graph execution plan. A compatible CLI must register requirements, decisions, phases and tasks before implementation begins.

Related first-effort scope: `docs/plans/relay-minimal-install-phase-1.md`. That effort removes unused VPN components from fresh relay-only installations; it does not implement this specification.

## Goals

- Manage physical Entry and Exit servers without learning ambiguous operating-versus-managed node categories.
- Manage one Entry forwarding different public ports to multiple Exits from one coherent administrator workflow.
- Assign customer-facing routes to plans in bulk and inspect exactly what subscribers will receive.
- Advertise maximum speed honestly and enforce it per registered device, including relay paths.
- Distinguish configuration saved in the panel from configuration successfully applied on servers.

## Current implementation findings

- `web/src/pages/admin/Nodes.tsx` derives operating membership from enabled chains with subscription groups, not live server availability. Offline exits may appear in that view while an online but unpublished Entry does not. The separate Entry/Exit views currently omit combined-role nodes.
- `web/src/pages/admin/NodeChainForm.tsx` creates one Entry-port-to-Exit-port chain at a time, places the Entry port under advanced settings, and assigns subscription groups in a separate request.
- The existing backend supports port-based Entry fan-out using multiple chains; one claimed transport/port maps to one fixed Exit endpoint. This is not same-port automatic load balancing.
- Actual subscription visibility is based on the plan's group and attached chains. Direct visibility, enabled state, protocol compatibility and readiness also affect the result.
- `api-server/internal/handlers/subscription.go` intentionally withholds relayed chains from speed-limited subscriptions because a fixed chain Exit port does not select the required speed-tier listener. `TestRelayedChainIsWithheldFromSpeedLimitedPlans` documents this behavior.
- `pkg/speedtier/speedtier.go` maps positive Mbps values to tier ports and clamps to 1–2000. Plan values and advertised speed must not silently diverge from enforceable limits.
- `node-agent/internal/shaper/shaper.go` currently shapes a tier/port on an Exit, so devices sharing that tier share its bandwidth cap. Merely adding tier forwarding ports does not satisfy per-device enforcement.
- Existing heartbeat health fields do not establish that every route's current desired forwarding policy was acknowledged by the Entry.

These findings are code observations. A read-only browser visit also confirmed the repeated chain creation workflow, hidden Entry port controls, broad node table and confusing operating membership. No live plan could be inspected because the local plan list was empty. Short-lived differences between dashboard and node freshness are not treated as a proven consistency bug without further diagnosis.

## Approved UX scope

### Server inventory

- Replace operating/managed category tabs with All, Entry and Exit views.
- Include combined-role servers in both relevant role views.
- Display server availability, route readiness and customer publication as separate concepts.
- Keep role, name, location, freshness and operationally useful summaries prominent; move detailed IP, DNS, SNI and resource facts into role-appropriate detail views or optional columns.
- Preserve selection/filter context when navigating and use consistent translated terminology.
- Expose connected routes and affected plans from either Entry or Exit details.

### Entry-centered route management

- Select an Entry once, inspect its complete port-to-Exit map, and add multiple Exit routes in one operation.
- Show public Entry hostname/port, destination Exit/listener, transport, plan assignments and readiness reasons.
- Allocate ports automatically by default, preview assignments before saving, and expose manual overrides without hiding the core port map.
- Show conflicts, offline endpoints and missing DNS/listener/SNI prerequisites before publication. Draft configuration may be saved while prerequisites are incomplete; do not label it ready.
- Display public route names separately from physical server identifiers.
- Allow bulk plan assignment/removal and clear impact previews before disruptive route changes.
- Retain opaque L4 forwarding. Entry must not authenticate VPN users, inspect SNI for routing, or terminate TLS/Reality.

### Plan availability and customer preview

- Select available routes directly and in bulk from the plan editor. Existing groups may remain reusable presets rather than a required concept for routine operation.
- Provide a plan-by-route management matrix for shared assignments.
- Preview customer-visible route name, location, configured per-device maximum speed and traffic multiplier.
- Show which configured routes are currently eligible for publication, and give explicit reasons for excluded or pending routes.
- Use the same route-eligibility projection for preview and real subscriptions; account/device validity checks remain part of actual subscription authorization.
- Do not advertise a guaranteed measured throughput. A maximum is a policy ceiling; Entry/Exit capacity and congestion are separate operational facts.
- Direct access remains explicitly distinguishable from relayed access; preserve existing valid direct behavior without making direct publication an accidental default for the new Entry-centered flow.

## Approved backend scope

- Consolidate plan route eligibility and projection so administrator preview and raw VLESS, Clash and Sing-box publication do not diverge.
- Introduce transactional, concurrency-safe multi-route creation and plan assignment. Preserve transport overlap claims and database uniqueness; no partially assigned batches or orphaned routes on failure.
- Preserve existing route identifiers, managed Entry DNS, Exit-owned Reality SNI and compatible legacy/direct subscriptions during transition.
- Generate and apply relay forwarding, Exit listener and speed policies together where needed. Keep technical speed/listener ports behind the logical route interface.
- Replace shared-tier shaping where necessary to implement genuine per-device speed policy. Do not declare this complete merely by adding more ports.
- Track desired and applied configuration generations/acknowledgments with freshness and route-specific readiness reasons. Define safe activation order; unacknowledged or failed updates must not be presented as effective.
- Preserve last-known-good configuration on failed application without claiming that it enforces a newly changed limit. Display and gate availability against the actually effective configuration.
- Provide Entry aggregate and route-level forwarding metrics needed to understand fan-out usage and bottlenecks. Do not assume ordinary VPN process connection metrics describe L4 forwarding activity.
- Share plan/route eligibility logic behind a small interface rather than duplicating chain, group, readiness and speed decisions across screens.

## Per-device speed contract

- The enforcement identity is the registered device's server-side identity, not its source IP or a shared tier port. L4 SNAT and multiple sessions must not merge unrelated devices into one cap.
- Two registered devices on the same plan each have their own configured maximum; they do not share an account-wide or tier-wide budget.
- Multiple simultaneous sessions for the same device must not multiply the promised cap.
- Feasibility and enforcement across simultaneous connections to multiple Exits is a required design/verification question. Do not silently apply independent per-Exit caps while advertising a device-wide cap.
- A lower observed speed because of infrastructure congestion does not prove a configured cap was applied.
- Unsupported protocol/path combinations must be explicitly shown as unsupported or pending, not quietly advertised with an unenforced maximum.
- The exact enforcement implementation is not yet selected. Do not assume existing Xray/tc shaping can identify devices or provide global device budgets without evidence.

## Design questions to resolve before implementation is claimed ready

- How can the Exit identify and shape authenticated device traffic, including multiple connections and relayed traffic?
- What mechanism enforces one device budget across multiple Exits? Demonstrate feasibility, or return to the user for approval of an explicitly narrower contract.
- How should the single speed field apply to upload/download: separate directional ceilings or combined throughput? This detail was not decided by approval.
- What supported protocols can provide the selected contract initially? Start from the existing VLESS Reality relay path and do not imply universal protocol support.
- How are active sessions and policy updates handled on downgrade, failure or stale acknowledgments? Do not claim immediate revocation or zero-downtime policy changes.
- What measurement tolerance, burst window and realistic test environment establish the bandwidth acceptance threshold?
- How should direct route assignments and existing group-based visibility be migrated without changing previously published customer endpoints?

## Required verification before completion

- One Entry exposes separate ports for multiple Exits with correct port-to-destination isolation; overlapping transport/port claims fail deterministically under concurrent requests.
- A failed batch leaves no partial routes, port claims or plan assignments.
- Entry and Exit role views include combined-role servers; availability and publication are not conflated.
- Plan route preview matches actual subscription output and rejection reasons across supported formats.
- For a plan capped at N Mbps, two independent devices are not forced to share one N Mbps tier cap when sufficient infrastructure bandwidth is available.
- Multiple sessions for one device cannot multiply the device cap. Include a multi-Exit scenario or obtain explicit approval for a narrower advertised contract before release.
- Relayed limited-plan routes are published only when the relevant enforcement and forwarding configuration is supported and applied.
- A failed/stale application is visible; a higher advertised maximum or lower promised cap is never falsely marked effective.
- Bulk UX works across the supported languages, with role-specific readiness and actionable empty states.
- Existing unmanaged/legacy/direct paths, DNS ownership, Reality SNI and valid customer route identifiers remain compatible through migration and rollback.

## Out of scope

- Same-port random/round-robin balancing, automatic failover and high availability.
- TLS/Reality termination or VPN user authentication on Entry.
- Automatic removal of programs from existing servers or role-change migration.
- Universal VPN protocol redesign without a separately validated enforcement capability.
- A promise of measured/guaranteed throughput or immediate client refresh/revocation.
- Implementation, deployment or modification of live admin data as part of this approval record.
