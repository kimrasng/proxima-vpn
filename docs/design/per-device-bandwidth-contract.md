# Per-device bandwidth implementation contract

## Confirmed behavior

The administrator selected per-registered-device maximum speed. Upload and download each have their own maximum N Mbps. All sessions and all Exits carrying the same registered credential identity share that direction's central budget. Separate devices do not share a tier cap.

## Implementation

- Exit Xray matches its authenticated VLESS email to a device-specific SOCKS outbound.
- The node agent materializes random local passwords and owns an authenticated loopback-only egress proxy.
- Before forwarding TCP chunks or UDP payloads, the agent requests a central permit with its node key, device UUID, direction and byte count.
- The API authorizes the node and current device entitlement, derives rate from the plan, and consumes an atomic Redis token bucket keyed by device UUID and direction.
- Redis time and one atomic script coordinate concurrent Exits. No node-local grant cache or independent per-Exit rate budget is used.
- Budget-service errors close or reject controlled traffic; there is no unrestricted Freedom fallback for the authenticated VLESS route.
- Canonical API configuration and digest exclude runtime secrets. Secret-bearing runtime configuration and backups require 0600 permissions.
- Legacy tier ports remain for existing client configuration compatibility, but no longer define the device bandwidth budget.
- Limited subscriptions remain VLESS Reality only and require a current config acknowledgment plus device-global shaping mode. Controlled relay paths use the same device routing on the Exit.

## Rate measurement

Limits measure forwarded application/tunnel payload. TCP charge excludes SOCKS handshakes; UDP charge excludes SOCKS headers. NIC framing, Reality/TLS framing, acknowledgments and retransmissions are not included. A bounded token bucket permits short bursts, so the contract is sustained throughput with an explicit burst allowance, not an instantaneous wire-rate limit. Physical device attestation is not provided; copied credentials share one device budget.

## Operational implications

- One five-second cache contains only previously verified node API-key authentication so per-chunk requests are not throttled as human actions; invalid keys are rate-limited before DB authentication. Device entitlement, speed and byte grants are never cached. Node-key rotation can take up to five seconds to invalidate that authentication cache.
- Each permit uses BEGIN, four entitlement SELECTs with shared device/user/plan locks, Redis for positive rates, then COMMIT. This intentionally serializes against assignment/rate changes, but is a correctness baseline with substantial control-plane cost. Control-plane latency and Redis/DB capacity may lower achieved throughput; N Mbps is an upper policy bound, not guaranteed delivery speed. Load testing and safe batching are prerequisites for large deployments.
- If the budget API or Redis is unavailable, VLESS traffic using the egress path stops. There is intentionally no fail-open cache.
- Old agents cannot materialize the egress template. Upgrade agents before activating the new API generation. Do not enable it fleet-wide against old agents without a staged cutover.
- Device routing changes require full Xray config application; that can disconnect sessions. Existing non-VLESS sessions are retired by config reload after a plan change, not instantaneous global revocation.
- The agent's loopback SOCKS TCP control channel is authenticated. SOCKS5 UDP packets do not carry credentials; stock Xray requests wildcard UDP association source ports. Source pinning and per-association random ports assume a trusted node-local process environment, not isolation from a hostile local process that races a valid first datagram. Do not expose the UDP relay externally.
- The egress dialer rejects private, loopback, link-local and node-local destinations to protect management listeners. This is not a LAN-access VPN feature.
- Entry remains opaque L4 and does not authenticate customers or host the limiter.

## Verification gate

Do not mark production ready until central budget concurrency, two independent devices, same-device concurrent sessions and multi-Exit sharing, both directions, UDP framing/association ownership, actual supported Xray Vision interoperability, failure closure, credentials revocation and downgrade behavior have evidence. Unit/config tests alone are not proof of network throughput or deployed behavior.

## Verification evidence

Actual Xray 26.3.27 Vision and XUDP scenarios passed through the production routing transformer and node-local limiter, including four sessions per device across two Exits and fail-closed shutdown/revocation. The datapath harness used one fake central budget per identity/direction; actual Redis concurrency and PostgreSQL authorization/plan-change ordering were independently verified with no required skips. Evidence and explicit boundaries: `tests/evidence/device-bandwidth/README.md`.

## Specification tracking

The existing spec-graph CLI still requires explicit legacy IDs whereas the operator contract requires generated IDs. This is a implementation contract, not a graph-native execution plan; no entity IDs are invented. Existing subscription impact analysis and graph validation were run before modifications.
