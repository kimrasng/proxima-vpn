# Signed release verification v1

The daemon requires a signed envelope for every new apply. There is no unsigned
mode. Root provisions its unchanged canonical bytes at the fixed path
`/etc/proxima-updater/release.json` (root 0600, atomic replacement, no trailing
newline), the policy below, and matching independent `approved.json`.
The socket still accepts only release ID and maintenance consent.

`tools/release-publisher` emits this v1 format as `release-envelope.json`;
an operator installs its unchanged bytes as `release.json`. No production key,
host enrollment, image download or deployment is performed by this integration.

## Trust and API

`releaseverify.NewVerifier(Policy)` copies one Ed25519 public key and a root
policy. `Verifier.Verify(envelopeBytes, Context)` is pure and concurrency-safe;
it returns `Verified` only after signature, format, scope, source pair, target
pair, time, floor and high-water checks. `Verified.Manifest()` returns a value
copy, `PayloadSHA256()` returns the authenticated payload identity, and
`Verified.Authorize(Approval)` checks independent root permission. A zero
Verifier or zero Verified cannot authorize anything.

The API is deliberately split: a signature proves publisher authenticity, not
root approval; verification computes eligibility, not durable consumption.
Calling Verify twice with an unchanged Context can succeed twice. The caller
must serialize and persist acceptance as specified below. No function fetches
a URL, pulls an image, executes a command, creates an approval, or saves state.

The daemon's `loadRelease` reads the policy ONLY from
`/etc/proxima-updater/release-policy.json` using `readTrusted`, and the bounded
envelope ONLY from `release.json`. Under the mutex, `approvedRelease` loads
the fixed `approved.json` and calls `verifyRelease`, the verification core also
used by the standalone `verifyApprovedRelease` adapter. It returns
`approvedRelease{approval, verified}`: use that exact approval snapshot for
execution, not a subsequent reread. The snapshot preserves a root decision to
disable rollback even when the publisher allows it. Root cannot enable rollback
when the signed transition forbids it. ID, both source references and both target
references must match exactly. No adapter input chooses a key, policy path,
approval path, algorithm, executable, registry, or URL.

The existing trusted-file loaders require root ownership, no group/other write,
no symlinks in any ancestor, a regular file and bounded reads. The adapter shares
that trust model: root and concurrent root file replacement are trusted; an
unprivileged user cannot replace a checked path component. It does not claim
protection from malicious root or a concurrently compromised root provisioning
process. Root policy/approval updates should use atomic replacement, never
in-place edits, and should not race an accepted operation. These existing
loaders were not changed or tested against real uid-0 files by this increment.

## Root policy provisioning

Provision `/etc/proxima-updater/release-policy.json` out of band, root 0600 in
the existing root-owned protected directory. The public key must be independently
authenticated, never copied from an untrusted manifest or fetched from a URL it
names. It is a **raw 32-byte Ed25519 public key**, standard padded base64, not PEM,
DER, an SSH key, a certificate or a private seed. No trust-on-first-use fallback,
algorithm negotiation, embedded key, key ID resolver or automatic key rotation
exists. NewVerifier rejects absent/malformed/zero keys and invalid policy.

Policy fields (all required in practice; no secure default exists):

| Field | Meaning |
| --- | --- |
| `public_key` | Authenticated 32-byte Ed25519 public key as padded base64 |
| `channel` | Root-selected `[a-z][a-z0-9_-]{0,63}`, e.g. `stable` |
| `api_repository` | Exact lowercase fully qualified API repository |
| `web_repository` | Exact lowercase fully qualified web repository, distinct from API |
| `minimum_sequence` | Root-selected minimum acceptable release sequence, 1 through 9007199254740991 |

Repository syntax is intentionally narrower than all OCI syntax: a dotted DNS
registry and slash-separated lowercase components, with internal `.`, `_`, or
`-` separators. No registry ports, credentials, schemes, tags, query strings,
fragments, traversal, empty components or Docker Hub shorthand. For example,
`ghcr.io/owner/project/api` and `ghcr.io/owner/project/web` are valid. Whole
repository equality is enforced, not a prefix match.

The floor is not a substitute for the durable high-water mark. Root enrollment
must explicitly establish a known current image pair and sequence (>=1), with a
floor appropriate to the trusted release history. Sequence 1 may represent the
enrollment baseline; the first update must then be >=2. Never initialize missing
state to zero or infer a baseline from an unverified release. An authenticated
bootstrap process for new hosts is still required outside this library.

## Exact signing format

The envelope is exactly the compact UTF-8 JSON encoding of `releaseverify.Envelope`
with fields in this order:

```json
{"payload":"STANDARD_PADDED_BASE64_PAYLOAD","signature":"STANDARD_PADDED_BASE64_SIGNATURE"}
```

The strings above describe positions, not usable credentials or a signed release.
Do not append a newline to the envelope. Maximum envelope length: 16384 bytes.
Base64 is canonical standard padded RFC 4648 encoding; CR/LF, alternate padding
bits, URL alphabet and missing padding are rejected.

The decoded payload must equal `encoding/json.Marshal(releaseverify.Manifest)`
byte for byte: this is a specific v1 encoding, **not** generic JSON canonicalization
or JCS. Field order is fixed, all fields are present, integers are decimal, no
whitespace/newline/BOM, no omitted false, and nested image fields are `api`, `web`.
Every accepted string is ASCII. Unknown, duplicate, case-aliased, escaped-alias,
null, missing, trailing and noncanonical encodings are rejected, even if signed.

| Position | Field | Constraint |
| --- | --- | --- |
| 1 | `schema` | Integer 1 |
| 2 | `product` | String `proxima-vpn` |
| 3 | `channel` | Exact pinned policy channel |
| 4 | `release_id` | `[a-z][a-z0-9_-]{0,63}`, globally unique operation ID |
| 5 | `sequence` | Integer 1..9007199254740991; above accepted high-water and >= floor |
| 6 | `issued_at` | Positive Unix seconds, <= trusted host clock |
| 7 | `expires_at` | Unix seconds, strictly after issue and current time; lifetime <=604800 seconds |
| 8 | `from` | Exact authenticated current API/web image pair |
| 9 | `to` | Exact target API/web image pair; at least one differs from `from` |
| 10 | `rollback_compatible` | Explicit JSON boolean |

Each image is `<pinned-repository>@sha256:<64 lowercase hex characters>`.
Both source and target roles must use their respective pinned repository. No
version tags are resolved. Each supported source-to-target transition needs its
own signed payload, including compatibility for that specific source schema.
The publisher must assign a globally increasing sequence within this product
and channel. Sequence, not a semantic version tag, defines release order.

Sign exactly:

```go
payload, err := json.Marshal(manifest) // releaseverify.Manifest
// Handle err; validate all fields and the intended source transition.
message := append([]byte(releaseverify.SignatureDomain), payload...)
signature := ed25519.Sign(privateKey, message)
envelope, err := json.Marshal(releaseverify.Envelope{
    Payload: base64.StdEncoding.EncodeToString(payload),
    Signature: base64.StdEncoding.EncodeToString(signature),
})
// Handle err and write these bytes without adding a newline.
```

The domain bytes are ASCII `proxima-vpn/server-updater/release/v1` followed by
one NUL byte, then the exact payload bytes. Use plain Ed25519, not Ed25519ph,
Ed25519ctx, or a signature over a digest string. The private key belongs only to
the trusted publisher/signing service; never provision it on the updater host.

Freshness assumes a trustworthy host clock. There is no clock-skew allowance.
This format cannot prove that a still-valid unseen release is the newest release
if a delivery service withholds newer releases; expiry bounds that freeze window
to seven days, and the floor/high-water reject previously accepted versions. A
compromised signing key can sign malicious new code or relabel old image content
with a higher sequence; that is outside publisher-authenticity guarantees. Root
can revoke a key out of band and raise the floor, but must never reset high-water
on key/channel/repository changes. No automatic recovery from exhausted sequence
space is supported.

## Durable journal v2 and explicit root enrollment

The journal is one `state.json` transaction: `version: 2`, `product: "proxima-vpn"`,
`policy` (the entire root policy), `current` (API/web pair), `current_known`,
`high_water`, `consumed` (IDs), `accepted` (approval/sequence/payload_sha256 records),
and `operation`. Each operation repeats its accepted sequence and payload hash.
The hash is lowercase hexadecimal SHA-256 of the authenticated payload, not the
envelope. Acceptance fsyncs a temporary file, renames it, then fsyncs the directory
before any Docker command. Any acceptance save error latches the controller
closed until restart, including an error after rename.

Before mutating Docker, a separate `recovery-required` marker and its directory
are fsynced. This marker carries no replay ledger: acceptance remains one atomic
transaction. It survives a terminal journal rename whose directory sync fails,
so containment cannot leave stopped containers behind a trusted success record.
Only a durably saved terminal outcome allows marker removal. If removal sync
fails, a fresh marker must be durable before any subsequent containment mutation.
On restart, an existing marker overrides even a terminal outcome to manual
recovery. Docker command errors never authorize automatic rollback; only a
recognized readiness failure following successful commands can do so, subject
to compatibility approval and an uncancelled operation.

Missing and v1 state remain readable for status and equal-pair bootstrap but
cannot authorize updates. Corrupt v2 state prevents startup. There is no automatic
migration, zero baseline or baseline inferred from release input. On a new host,
root independently authenticates the current pair and a sequence >=1, stops the
daemon and atomically provisions v2 with that pair, `current_known: true`, the
exact policy, empty `consumed`/`accepted` arrays and `operation: null`. Sequence 1
is appropriate only when independently known; the first transition must exceed it.
Existing histories require explicit root reconciliation preserving all consumed
identities and high-water. Unreconstructable trusted history blocks migration;
it must not be replaced by an empty ledger.

For recovery, root stops the daemon, reconciles runtime and database, preserves
`high_water`, `consumed` and `accepted`, updates `current` after verifying the
pair, sets `current_known: true` and clears `operation`. Never archive/delete
state to clear recovery. After syncing that reconciled journal, remove the
`recovery-required` marker and sync its directory before restarting the daemon.
The daemon rejects a cleared operation with a remaining marker. Unknown pairs and preflight rejection block new applies
until reconciliation. Success records `to`; verified rollback records `from`;
neither failure nor rollback reduces high-water. Interrupted operations enter
manual recovery without resuming. Recorded-ID retries return the stored result
without reading a replacement or expired envelope.

Every policy change, including key or floor, requires explicit root journal
reconciliation with the new policy while retaining high-water and history.
Policy mismatch fails closed; rotation never silently creates a fresh ledger.

Both target and rollback API images must implement `/ready`. Bootstrap preflight
checks Docker identity/topology only, not HTTP readiness. A source lacking
`/ready` is not eligible for automatic rollback even if its database schema is
compatible; root must disable rollback for such a transition.

## Daemon integration invariants

The acceptance path implements these coordinated invariants:

1. **`store.go`: migrate the durable schema explicitly.** Add the trusted current
   pair and high-water (>=1), accepted payload SHA-256 and sequence to the journal.
   Retain consumed IDs and approval snapshots. Bind persisted state to the root
   product/channel/repository policy. Missing, corrupt, mismatched or old-schema
   state must block signed apply pending explicit root enrollment/migration; do
   not use `Store.load`'s current missing-file default as the signed baseline.
   Save high-water, payload identity, consumed ID and accepted operation in ONE
   atomic fsynced journal transaction. A separate sidecar high-water write and
   the old state write leave a crash window and are not acceptable.
2. **`main.go`: load fixed local signed release input.** Provision the bounded
   envelope in a root-owned fixed path such as
   `/etc/proxima-updater/release.json` and read it with the protected-path/regular
   file/bounded-read discipline. Do not make its path a socket parameter. Keep
   the lifetime flock and root checks. Load policy/approval using the adapter;
   there must be no unsigned fallback after signed mode is activated.
3. **`controller.go`, `Controller.accept`: under `c.mu` and lifetime flock**, keep
   the existing busy/recovery/consumed-ID checks; take Context from the trusted
   journal and host clock, never from the socket body or manifest. Call
   `verifyApprovedRelease` before accepting. Compare its approval ID with the
   requested ID. Independently confirm the current runtime pair through the
   existing Docker preflight/identity checks before downtime. Journal the
   verified sequence as the new high-water with the accepted operation BEFORE
   any Docker mutation. On persistence failure, run nothing. Execute the exact
   returned `approval` snapshot; do not reconstruct rollback policy from the
   manifest or reread approval after verification.
4. **Idempotence/crashes:** repeated socket requests for the current recorded ID
   may return recorded status without executing or re-verifying an expired
   manifest. They must never substitute a newly supplied payload for the stored
   payload identity. Interrupted operations still enter manual recovery; do not
   resume execution by revalidating an already consumed sequence. Verify alone
   is not a mutex or replay ledger.
5. **Success/failure/recovery:** current pair advances to `to` only after verified
   success and remains/returns to `from` after verified rollback. An uncertain
   pair blocks all new applies. High-water never decreases on rejection after
   acceptance, failed apply, rollback, crash, policy rotation or recovery. The
   old INSTALL.md instruction to archive `state.json` cannot serve as signed
   recovery: migrate/reconcile state while preserving high-water and consumed
   identity. New retries require a fresh publisher sequence and root approval.
6. **Preflight/bootstrap:** preflight must be read-only and must not imply signed
   update availability from root approval alone. Preserve equal-pair manual
   bootstrap for enrollment, separately from signed transitions; equal pairs
   cannot pass Verify or authorize an update. Keep the socket payload limited
   to ID plus maintenance consent. No keys, manifests, URLs or image references
   should be added as executable authority to the socket protocol.
7. **Add integration tests** at controller/store boundaries before enabling:
   absent/invalid signatures cause zero Docker mutation; concurrent accepts
   consume only once; journal failure prevents effects; restart/rollback/root
   recovery retain high-water; wrong source blocks; altered approvals and
   post-verification replacement cannot change the accepted snapshot; legacy
   state has no silent unsigned fallback. Existing migration/readiness/rollback
   tests remain mandatory.

## Publisher and operator integration

The separately maintained publisher workflow must give
API/web build steps IDs, capture their immutable digest outputs, and form the
complete repository@sha256 references from those exact build outputs, not
mutable tags. Publish a manifest for each reviewed supported source transition
using a protected monotonic sequence allocator, short validity and reviewed
migration/partial-migration rollback compatibility. Sign with the independently
provisioned publisher key; publish the unchanged envelope as a release asset.
The signing job needs least-privilege access to that key/signing service; a
registry login token is not a publisher signature.

Operators obtain the envelope through their deployment process, authenticate it
locally, review root approval, and preload the exact digest images. The current
daemon never pulls/builds and keeps `--pull never`. Registry digest addressing
binds the bytes Docker loads to the signed references; the parser does not hash
local layers or validate the database migration semantics. Digest signatures do
not make mutable tags, a different repository, or a different source transition
acceptable. Signed compatibility is necessary but does not remove the root
operator's independent migration review and downtime approval.

## Verification scope

Run from `tools/server-updater`:

```sh
GOWORK=off go test -race -shuffle=on -count=1 ./...
```

Tests use deterministic private seeds **only in test code**, real Ed25519 signing
and verification, fixed clocks, malformed/signed-policy-negative cases and exact
root approval binding. Existing updater tests still execute. Controller and journal
tests use real signatures, temporary files and a fake Docker executor. This is not
real-root filesystem, publisher CI, image download, deployment, or end-to-end
production update evidence.
