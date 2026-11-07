# Control protocol v1 (for the future parent-API integration)

Transport: HTTP/1.1 over the Unix socket `/run/proxima-updater/control.sock`
(root, group = configured `socket_gid`, mode 0660). There is no TCP listener.
Authorization = socket group membership. Nothing in the protocol carries a
credential, so never mount the socket anywhere less trusted than the API
process itself.

The managed API receives `/run/proxima-updater` as a fixed read-only directory
bind with `rprivate` propagation and the numeric `socket_gid` as a supplementary
group. It uses this same socket path inside the container. Read-only restricts
filesystem mutation, not HTTP requests over the socket. The directory bind and
systemd directory preservation allow reconnection after socket replacement;
clients must discard stale connections after updater restart. The web container
receives no socket access, and no container receives Docker's socket. See
`INSTALL.md` for UID/GID, boot ordering and existing-runtime re-enrollment.

Common rules for every request:

- Query strings are rejected. Unknown paths return `404`. Methods other than
  the ones below return `404`.
- Request bodies are capped at 1024 bytes, must be `Content-Type:
  application/json`, and are parsed with strict JSON: unknown fields,
  duplicate keys, and trailing data are all rejected with `400
  {"error":"invalid_request"}`. The API can never send commands, paths,
  image references, Compose arguments or any other executable content - the
  only fields that exist are `release_id` and `maintenance`.
- All responses are `application/json`, `Cache-Control: no-store`, and never
  contain environment values, subprocess output or other secrets.

## GET /v1/status

Read-only snapshot. Always safe; never mutates anything.

```json
{
  "version": 1,
  "consumed": ["release-one"],
  "operation": {
    "approval": {
      "id": "release-one",
      "from": {"api": "ghcr.io/x/api@sha256:...", "web": "ghcr.io/x/web@sha256:..."},
      "to":   {"api": "ghcr.io/x/api@sha256:...", "web": "ghcr.io/x/web@sha256:..."},
      "rollback_compatible": true
    },
    "phase": "succeeded",
    "maintenance": false,
    "migration_uncertain": true,
    "error": ""
  }
}
```

`operation` is `null` before the first apply. Phases, in order:
`accepted` -> `stopping` -> `applying` -> `succeeded`, or `rejected`,
`rolling_back` -> `rolled_back`, or terminal `manual_recovery`.
Terminal phases: `succeeded`, `rolled_back`, `rejected`, `manual_recovery`.

Field semantics the API must respect:

- `maintenance: true` - the managed pair is (or may be) down; show the
  maintenance screen to end users.
- `migration_uncertain: true` - the database may hold schema state from the
  target image, including partially committed migrations. Treat any
  non-`succeeded`/`rolled_back` state as "needs a human", and never offer a
  one-click retry: retries are refused by the controller anyway until
  recovery.
- `error` - a stable machine token (`preflight_failed`, `stop_failed`,
  `stop_unverified`, `apply_failed`, `rollback_stop_failed`,
  `rollback_stop_unverified`, `rollback_failed`, `persistence_failure`,
  `interrupted_operation`, `recovery_stop_unverified`), never free text.
  Recovery attempts immutable-ID stops on identified managed containers, but
  `maintenance` never proves that a timed-out Docker request cannot start a
  container later. Root reconciliation is required even after verified stops.

## GET /v1/preflight

Read-only check: does the deployment match the managed runtime and does the
current approval line up, without changing any container. This also verifies
manual enrollment with an equal-pair bootstrap approval. A successful result
alone does not mean an update is available or authorized.

```json
{"ok": true, "reason": ""}
```

`reason` when `ok` is false: `busy` (an operation is running), `manual_recovery`
(a previous operation needs human recovery), `not_approved` (no/invalid
approval file - the API must offer nothing), `topology_mismatch` (deployment
is not the enrolled managed runtime - typically after an operator recreated
containers by hand), `unavailable` (Docker error; retry later).
An equal-pair bootstrap enrollment file can return `{"ok":true,"reason":""}`
after manual enrollment. The same file is valid for `--render-bootstrap`,
but `POST /v1/apply` still returns `403 not_approved`: an update must change
at least one pinned image. Preflight does not consume approval or probe HTTP
readiness; verify service HTTP and Caddy routing separately.
An unenrolled host answers `topology_mismatch`; the controller cannot and will
not enroll it.

## GET /v1/candidate

Read-only observation of the currently prepared, root-approved signed release.
No query or request body is accepted. Success is HTTP 200 with exactly:

```json
{
  "status": "prepared",
  "release_id": "release-one",
  "sequence": 2,
  "from": {"api": "ghcr.io/x/api@sha256:...", "web": "ghcr.io/x/web@sha256:..."},
  "to": {"api": "ghcr.io/x/api@sha256:...", "web": "ghcr.io/x/web@sha256:..."},
  "rollback_compatible": false,
  "expires_at": 1800003600
}
```

`expires_at` is Unix seconds. Rollback permission is the effective root approval,
which may be stricter than the signed manifest. The response omits trust policy,
keys, envelope, payload hash, replay history and host configuration. It is a
separate DTO from both the preparation CLI receipt and status journal.

The host verifies the installed protected envelope, independent root approval,
current pair, sequence and expiry, and inspects cached target images and managed
source topology. It rechecks signed identity and expiry after inspection. Busy,
recovery, missing preparation, invalid/expired signature, mismatched approval or
policy, consumed release, missing images and failed inspection all return HTTP
503 `{"error":"candidate_unavailable"}` with no candidate details. Lock contention
also fails closed rather than queueing observations. Docker inspection uses the
request context with a four-second timeout. No images are pulled, no state is
written, no approval is created and no container is changed.

This observation is not a reservation or a guarantee of later apply success.
The owner API exposes it at `GET /api/admin/system-updates/candidate` after a fresh
active-owner check and strict response validation. API and host clocks must be
synchronized; the API rejects expired or more-than-seven-days-away expiry values.
All responses are `no-store`. Apply still requires explicit maintenance consent,
password/TOTP reauthentication at the owner API, and independent host verification.

## Release preparation (opt-in publisher-signed mode)

By default the updater runs in `manual` authorization mode and the endpoints
below fail closed: `POST /v1/prepare` answers `403 {"error":"not_authorized"}`.
Nothing about the manual mode changes.

Root may opt in per host by writing
`/etc/proxima-updater/authorization-policy.json` (root-owned, no group/world
write anywhere in its path, no symlinks, max 1 KiB, strict JSON):

```json
{"mode":"publisher_signed","allow_rollback":false}
```

`mode` is required and must be `manual` (the default when the file is absent)
or `publisher_signed`. `allow_rollback` is a separate root permission; it is
rejected in `manual` mode and never implied by a signed
manifest. Any other field, duplicate key, unknown value, or trailing data makes
the whole policy invalid, and every authorization check then fails closed until
root fixes the file. Preparation rechecks the policy after its long steps.
Acceptance rechecks the policy under the controller lock before journaling;
after acceptance, that operation keeps its captured authorization. Revocation
affects new acceptances and does not cancel or change an accepted operation.

### POST /v1/prepare

```json
{"tag":"v1.2.3"}
```

The only request field is `tag`, matching `v[0-9][A-Za-z0-9._-]{0,126}`. No URL,
path, image reference, key, credential, or any other field exists; the
repository (`github.com/kimrasng/proxima-vpn`) and asset name
(`release-envelope.json`) are compiled in and identical to the CLI preparation
client, including its single-redirect HTTPS shape and size/timeout limits.

| Code | Body | Meaning |
|---|---|---|
| 202 | `{"status":"preparing","tag":"v1.2.3"}` | verification and pull started on one background worker; poll `GET /v1/prepare` |
| 400 | `{"error":"invalid_request"}` | malformed tag/body, query string, oversized or non-JSON body |
| 403 | `{"error":"not_authorized"}` | manual mode, invalid policy, or trust policy does not match the enrolled journal |
| 409 | `{"error":"blocked"}` | another preparation or apply is active, a recovery gate is open, or the journal is not reconciled |

Preparation is bounded (10 minutes total), serialized against apply and
recovery through the same controller mutex and `active` flag, and cancelled by
daemon shutdown. It never consumes a release, writes the replay journal, or
stops/starts containers; it only pulls the two approved target digests and
verifies them plus the running source topology. The verified envelope is
stored as `/var/lib/proxima-updater/prepared-release.json` (root-owned regular
file, mode 0600, no symlink) via the same fsync+rename+directory-sync path as
the journal. On any failure - including an uncertain final durability write -
no candidate is reported, and an uncertain write additionally blocks the
controller until restart, exactly like an apply journal failure.

### GET /v1/prepare

```json
{"status":"prepared","tag":"v1.2.3","candidate":{"status":"prepared","release_id":"release-one","sequence":2,"payload_sha256":"<64 hex>","to":{"api":"ghcr.io/x/api@sha256:...","web":"ghcr.io/x/web@sha256:..."},"expires_at":1800003600}}
```

`status` is `idle`, `preparing`, `prepared`, or `failed` (with
`"error":"preparation_failed"`). This is a volatile observation of the current
preparation job only; it is not stored across restarts. The durable surface is
`GET /v1/candidate`, which re-verifies the persisted envelope from scratch
after a restart, including signature, pinned policy, current pair, high-water,
expiry, and image presence. In `publisher_signed` mode the candidate response
additionally carries `payload_sha256`, the SHA-256 of the exact signed payload
bytes; the manual-mode response is byte-identical to before and omits it.

### POST /v1/apply in publisher-signed mode

```json
{"release_id":"release-one","maintenance":true,"payload_sha256":"<64 lowercase hex>"}
```

The third field `payload_sha256` is required in `publisher_signed` mode and
forbidden (400) in `manual` mode, which keeps the manual request byte-identical
to the documented two-field contract. The updater never takes the release
content, images, or approval from the request: it reloads the protected
envelope from the state directory and re-verifies signature, pinned trust
policy, current running pair, high-water sequence, expiry, and the root policy,
then requires the request hash to equal the SHA-256 of the verified signed
payload before journaling acceptance. A same-ID envelope swapped between
prepare and apply therefore cannot be applied: the hash binds the apply to the
exact prepared bytes, and the idempotent replay path checks the same hash
against the journaled operation. Automatic rollback runs only when the
separate root `allow_rollback` permission is present AND the signed manifest
declares the transition compatible; otherwise a failed apply stops in
`manual_recovery` exactly as in manual mode.

## POST /v1/apply

```json
{"release_id": "release-one", "maintenance": true}
```

Both fields required. `maintenance: true` is the explicit downtime consent -
the request is rejected without it. `release_id` must match the id in the
root-written approval file (`[a-z][a-z0-9_-]{0,63}`); the updater loads the
approval itself and never accepts image references from the request.

Responses:

| Code | Body | Meaning |
|---|---|---|
| 202 | the current state JSON | operation accepted and journaled; it runs to completion regardless of this connection (client disconnects/timeouts do not cancel it) - poll `/v1/status` |
| 400 | `{"error":"invalid_request"}` | malformed body, missing consent, oversized, unknown/duplicate fields |
| 403 | `{"error":"not_approved"}` | no approval file matches `release_id`, or its content is invalid |
| 409 | `{"error":"blocked"}` | an operation is active, a `manual_recovery` gate is open, or the approval was already consumed |
| 500 | `{"error":"persistence_failure"}` | the durable state could not be written - nothing was started, nothing will run |

Idempotence and replay: a repeated POST with the same `release_id` while that
operation remains current, including after a terminal outcome, returns `202`
with the recorded state and never runs it twice. Once another operation is
current, a previously consumed release ID is rejected. One approval equals
exactly one attempt, including rejections.

## Client implementation notes (parent API)

1. Expose an admin-only "apply approved release" action backed by
   `POST /v1/apply`, rendering `maintenance` as a full downtime confirmation.
2. Poll `/v1/status` (e.g. every second) and mirror `phase`, `maintenance`
   and `error` verbatim; surface `manual_recovery` as an operator alert, not a
   retry button.
3. Check `/v1/preflight` before applying a separately approved update; never
   treat preflight success alone as evidence of an available update, and never
   cache its result across an apply.
4. Never broaden the request type: any new capability (e.g. a future signed
   release manifest) must extend the approval file format and the controller,
   not the socket request. The socket deliberately carries no authority
   beyond "apply what root approved".
