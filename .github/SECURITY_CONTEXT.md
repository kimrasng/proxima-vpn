# Proxima VPN — security context for automated PR review

Fed to the PR reviewer via `repo_context_files` in [`.pr_agent.toml`](../.pr_agent.toml)
so findings are judged against this repo's real trust boundaries instead of
generic advice. Keep it short; it is prompt context, not documentation.

## Trust zones

| Zone | Code | Trusted? |
|---|---|---|
| Admin panel / user portal (browser) | `web/` | No — user-controlled |
| API server | `api-server/` | The enforcement point |
| node-agent (runs on each VPN node) | `node-agent/` | Semi-trusted; authenticated by node API key |
| Install/update scripts | `scripts/` | Execute as root on nodes |

Go workspace: `api-server`, `node-agent`, `pkg`. DB is **pgx with `$1`
parameterized queries**. HTTP layer is Fiber.

## Where the controls actually live

Auth is enforced by **middleware on route groups**, declared centrally in
`api-server/internal/server/routes.go` — not inside individual handlers. A new
route registered in the wrong group is an authorization bypass.

- `api-server/internal/middleware/jwt.go` — admin JWT; re-checks the admin row in DB each request so revocation works.
- `api-server/internal/middleware/user_jwt.go` — user JWT; re-checks `is_active`/`status`.
- `api-server/internal/middleware/node_apikey.go` — node auth via `X-Node-Key`, **constant-time** comparison.
- `api-server/internal/handlers/admin_auth.go`, `user_auth.go`, `admin_2fa.go` — login, JWT issuance, TOTP.

Removing a DB re-check, or switching the key comparison to `==`, is a real finding.

## Resource ownership (highest-value bug class here)

The established pattern scopes every query to the caller:

```go
DELETE FROM devices WHERE id = $1 AND user_id = $2
```

A handler that takes an id from the path/body and queries `WHERE id = $1` alone,
with no ownership check, lets any authenticated user reach another user's row.
See `user_device.go`, `user_portal.go`.

## node-agent ↔ API server boundary

`api-server/internal/handlers/node_agent.go` serves node configs (Reality private
keys, UUIDs, Shadowsocks passwords) and ingests `Stats`, `Heartbeat`,
`ReportTLSCert`.

Data flowing **from** a node is attacker-controlled if that node is compromised.
Traffic stats drive billing and quota, so unvalidated, negative or overflowing
byte counts — or a node reporting stats for users not assigned to it — are real
findings.

## Secrets

Stored in `api-server/internal/database/schema.go`: `nodes.api_key`,
`nodes.reality_private_key`, `nodes.ss_password`, `devices.wg_private_key`,
`devices.xray_uuid`, `users.sub_token`; JWT secret comes from config.

**Legitimate delivery — not a leak:** WireGuard private keys and Shadowsocks
passwords go to the subscribing device; Reality private keys go to the node.

**Real findings:** a secret reaching a different principal; a secret in logs,
error strings or metrics; a secret newly added to a broadly-reachable response;
weakened generation (`math/rand` instead of `crypto/rand`, shortened tokens).

## Public / token-only endpoints

`/sub/:sub_token/:device_id` → `api-server/internal/handlers/subscription.go`.
Unauthenticated and rate-limited; the only guard is the secret `sub_token`
joined against the device. If a change lets `device_id` be fetched without
binding it to that token's user, it is cross-user secret disclosure.

Also public: `/health`, `/metrics`, and static `/scripts`, `/downloads`,
`/uploads`.

## Dangerous sinks

- **os/exec & shell** — node-agent shells out to manage Xray/WireGuard; interpolating panel-supplied strings into a command is command injection.
- **File paths** — existing code whitelists OS/arch (`node_agent_update.go`) and timestamps upload filenames (`admin_upload.go`). New path building from request data without a whitelist is path traversal.
- **Outbound HTTP** — existing calls use hardcoded URLs; a URL built from DB/user input is SSRF.
- **SQL** — parameterized throughout. Only `fmt.Sprintf`/concatenation into a query counts.
- **Config generation** — `api-server/internal/services/xray_config.go`.

## Known and accepted

`scripts/update.sh` and `scripts/install.sh` download the node-agent binary over
HTTPS **without checksum or signature verification**. Pre-existing and known —
do not re-report unless a diff makes it materially worse (plain HTTP, untrusted
URL/version source, removed check, or widened trigger permissions).

## Race / ordering

Device-limit enforcement in `user_device.go` counts then inserts without a
transaction — a known TOCTOU window. Report new check-then-act on quota, device
limits, expiry, node registration or token reuse when check and write are not in
one transaction. Also flag secrets returned or state mutated **before** the
ownership check completes.
