# release-publisher

Publisher side of the server-updater's signed-release format
(`tools/server-updater/releaseverify` v1). It builds the canonical
`repo@sha256` release manifest for one reviewed source transition, signs it
with the publisher's Ed25519 key, re-verifies the exact envelope bytes with
the real verifier library, and writes the envelope atomically.

It is a pure signer: no registry client, no Docker, no network, no host
access. The GitHub workflow `.github/workflows/release.yml` is its only
intended consumer.

## Activation is blocked

The workflow deliberately fails `signing-readiness` before the signing job
can start. On 2026-11-04 the environment and main branch protection API
queries both returned HTTP 404; that does not prove whether controls are
absent or inaccessible. No production key was read. A workflow variable,
manual checkbox or operator assertion cannot bypass this gate.

Before a reviewed change removes the failing gate, an administrator must
provide independently reviewable evidence of all these external controls:

- `release-signing` exists with required reviewers, prevent-self-review,
  no administrator bypass, and a deployment policy allowing only the
  **branch** `main` (no tag or wildcard policy).
- `RELEASE_SIGNING_KEY` exists **only in that environment**; no repository
  or organization copy is accessible to arbitrary branch workflows. Move
  and rotate any previously exposed repository key; provision its matching
  public key on hosts. Do not put key values in evidence.
- `main` requires independent review, including the workflow, publisher,
  verifier dependency and all build definitions; force pushes, deletion and
  bypass are forbidden. Release tags cannot be moved or deleted. Third
  party actions are pinned to reviewed commits.
- Only this publisher may write envelope assets. Release/envelope history
  cannot be deleted or replaced by another workflow, token or administrator
  in the supported threat model. Otherwise GitHub assets are not a durable
  monotonic ledger and publication must move to an external append-only
  ledger with transactional sequence allocation before enabling signing.

YAML cannot prove these controls or constrain a different malicious
workflow. The disabled gate is an intentional deployment blocker, not
evidence that the repository is secure. The local signer remains usable
with a local test key; it does not claim to prove GitHub settings.

## Automatic publication assessment (2026-11-05)

**Blocked on external signing authority and durable allocation.** Publishing
a GitHub release is not yet sufficient to produce an installable envelope.
This assessment used repository code only; it did not query live GitHub,
inspect secrets, change settings, or enroll a host. The earlier HTTP 404
observations above remain historical evidence, not a current settings check.

The existing gate must stay closed for these concrete reasons:

- `sign-manifest` builds the signer from `github.sha` and requires the tag
  to equal that same commit. Its `go.mod` replaces the verifier dependency
  with `../server-updater`. There is no separately pinned, independently
  authorized signer revision. Automatically running this job for a tag
  would execute tag-controlled code with the signing key. Pinning checkout
  alone does not protect a key if another workflow can request the same
  environment. The existing per-run reviewer requirements also cannot be
  silently removed to meet the no-per-release-approval requirement.
- `verify-build` proves that bytes came from a specific successful first
  attempt and immutable artifact. It does not prove that the workflow or
  Dockerfiles at an arbitrary tag were trusted: that job can manufacture
  `image-digests.json`. The current same-reviewed-commit restriction supplies
  that missing trust assumption; accepting arbitrary tag commits removes it.
- `max-sequence` reads unsigned canonical payloads and the workflow starts
  with zero when no assets are found. Listing mutable release assets cannot
  prove that a higher sequence was never published and then deleted.
  Concurrency and upload-without-clobber do not make that history durable.
  Host high-water protects each host's accepted history, not publication
  order for a host that has not seen a deleted release.
- Each v1 envelope authenticates exactly one `from` pair. A release containing
  only B-to-C cannot update a host still on A. The current consumer downloads
  one fixed `release-envelope.json`; neither an arbitrary source wildcard
  nor a collection of envelopes is part of this contract. Source-specific
  migration compatibility cannot be inferred from tag order or image hashes.
- The verifier rejects expired envelopes, with a maximum lifetime of seven
  days. An old GitHub release cannot remain installable indefinitely using
  its original envelope. Re-signing needs a fresh sequence and release ID,
  authenticated source compatibility and durable publication, not an expiry
  bypass or an overwrite of the old envelope.

### Smallest safe implementation proposal

Establish one separately controlled publisher authority, then wire the tag
build to it. This is a proposal, not an implemented or provisioned service:

1. Pin the publisher executable, verifier and build-validation policy to an
   independently approved immutable revision outside tag control. Keep the
   Ed25519 key solely inside that authority. Authenticate owner publication
   requests and restrict the authority to the repository, channel and image
   roles; a valid workflow path alone must not authorize signing. One-time
   provisioning must identify who can change this policy and enroll the
   matching public key on hosts. Subsequent authorized owner publications
   need no PR approval or manual signing-environment approval.
2. Retain authenticated run/artifact verification, but obtain target digests
   from a separately trusted build definition or verify the tag's complete
   build definitions against the authority's approved policy before accepting
   its output. Do not execute tag code in the key-holding process.
3. Give that authority a durable transactional journal with exclusive write
   access. Allocate sequences above its persisted maximum, record the exact
   request, source/target pairs, compatibility decision and envelope bytes
   before exposing them, and retain consumed reservations after failure.
   An idempotent retry republishes the recorded bytes; an uncertain upload is
   reconciled without reusing a sequence for another payload. Missing or
   unreconstructable journal history blocks signing. Release assets are
   delivery copies, not the allocation authority.
4. Authenticate an enrollment baseline and a supported source-transition
   catalog. Issue separate v1 envelopes for each supported source pair,
   with unique identities and increasing sequences. Supporting skipped
   versions requires an updater delivery contract that can select the exact
   source-specific envelope; unsupported migrations must stay unavailable.
   That consumer change is outside this publisher-only scope. Rollback
   compatibility defaults to false and is authorized per transition.
5. Renew still-supported transitions before expiry with fresh identities and
   sequences, keeping earlier envelopes immutable. This also needs a consumer
   delivery contract for locating the current renewal. Preserve the seven-day
   verifier limit, host clock checks and durable host high-water on success,
   failure and rollback. Renewal must not broaden the supported sources.

The required external inputs are therefore an independently controlled signer
revision and key-access policy, an authoritative durable journal with a trusted
initial sequence, and an authenticated baseline/compatibility catalog. None is
established by the current publisher files. Removing `signing-readiness`, using
the latest tag as the source, using a run number as a replacement ledger, or
extending expiry would leave these blockers unresolved. Per-release root
approval and host preparation are separate updater responsibilities and cannot
be removed by a publisher change.

## One-time enrollment

1. Generate the publisher key pair **locally, once**:

   ```sh
   cd tools/release-publisher
   GOWORK=off go run . keygen
   ```

2. After the activation review above, store the `private-key` value as the
   `release-signing` **environment** secret `RELEASE_SIGNING_KEY`.
   Never commit it, paste it into chat, or provision it on any updater host.

3. Provision the `public-key` value into
   `/etc/proxima-updater/release-policy.json` on every host
   (`public_key` field), following `tools/server-updater/SIGNED_RELEASES.md`
   for the rest of the policy (channel, repositories, minimum sequence).

   ```sh
   PROXIMA_RELEASE_SIGNING_KEY='<private key>' \
     GOWORK=off go run . public-key
   ```

The key is read **only** from the environment variable
`PROXIMA_RELEASE_SIGNING_KEY`. There is no flag or file input on purpose:
arguments and files leak through process listings, CI logs and shell
history. Any absent, empty, malformed, zero or internally inconsistent key is
a hard error and no output file is written.

## Release flow

1. **Tag push** (`v*`): the Release workflow builds and pushes the API and
   web images, records the **immutable digests of the actual build outputs**
   into an immutable Actions artifact (also copied to the release for
   human inspection), and publishes the node-agent
   binaries.
2. **Manual sign** (Actions → Release workflow → "Run workflow"): an operator
   selects `main` and supplies the release tag, successful tag-push run ID,
   the exact API/web pair each updater host
   currently runs, a sequence number, channel and validity window. The
   `sign-manifest` job:
   - currently fails unconditionally at the external-control readiness gate,
   - after activation, obtains the key only inside the protected environment,
   - requires the tag to resolve to the exact selected main commit; old tags
     require a new reviewed release rather than executing old publisher code,
   - checks authenticated GitHub run identity (repository, push event,
     workflow path, tag, commit, success, first attempt only), artifact linkage
     and expiry, and the downloaded ZIP against GitHub's SHA256 digest,
   - extracts only a bounded regular `image-digests.json`, verifies its
     repository/ref/commit/run/attempt and role-specific image repositories,
     and signs only that verified output; mutable release JSON is never an
     authority and missing/expired artifacts cannot fall back,
   - fails if the sequence is not strictly above the highest sequence in
     every previously published `release-envelope.json` asset,
   - signs the recorded digests (never the mutable tags), self-verifies with
     the real `releaseverify` verifier, and uploads the envelope to the
     release. A second upload for the same tag fails instead of
     overwriting. The sign step prints the from/to pairs it signed: review
     that the `to` pair matches the digests recorded for that tag before
     root writes the matching approval file.
3. **Host side** (out of scope here, see SIGNED_RELEASES.md): root reviews
   and writes the matching approval file, preloads the exact digest images,
   and the updater consumes the envelope.

A signature is publisher authenticity, **not** root approval, not migration
review, and not downtime consent. Hosts still require the independent root
approval and their own durable high-water mark.

## Sequence allocation

The verifier orders releases by `sequence`, never by tag, and hosts reject
any sequence at or below their persisted high-water. GitHub Actions has no
protected, append-only counter, so the sequence is an explicit operator
decision. After activation a constant repository-wide concurrency group
holds the lock from before the ledger read through signing and completed
upload, with `cancel-in-progress: false`. GitHub may replace pending jobs;
this is mutual exclusion, not a FIFO guarantee. Duplicate uploads fail
without `--clobber`; a retry rereads the ledger and cannot reuse a published
sequence. An uncertain upload outcome must be reconciled before retrying.
All other publication writers must honor this same boundary.

The discipline:

- find the highest sequence already published (the workflow's ledger check
  does this automatically from all `release-envelope.json` release assets),
- allocate strictly above it,
- treat the release asset history as the ledger; if assets were deleted or
  its completeness cannot be proven, stop publication and reconcile an
  independently trusted record. Host high-water alone does not prove the
  publisher ledger is complete.

Sequences below 2 are refused: hosts keep a durable high-water that starts
at 1 at enrollment, so a first real transition needs at least sequence 2.

## Commands

```sh
# sign (key only from the environment)
GOWORK=off go run . sign \
  --from-api 'ghcr.io/OWNER/REPO/api@sha256:<64 hex>' \
  --from-web 'ghcr.io/OWNER/REPO/web@sha256:<64 hex>' \
  --to-api   'ghcr.io/OWNER/REPO/api@sha256:<64 hex>' \
  --to-web   'ghcr.io/OWNER/REPO/web@sha256:<64 hex>' \
  --tag v1.2.3 --channel stable --sequence 7 --validity 72h \
  --rollback-compatible=true \
  --out release-envelope.json

# read the highest sequence in a set of envelope files (ledger check)
GOWORK=off go run . max-sequence release-envelope.json ...

# enrollment helpers
GOWORK=off go run . keygen
PROXIMA_RELEASE_SIGNING_KEY=... GOWORK=off go run . public-key
```

Validation and fail-closed behavior, all before any file is written: key
presence and shape, ref syntax (`<repository>@sha256:<64 lowercase hex>`,
same repository per role for from and to), channel and release ID patterns,
sequence range, validity window (positive, at most 168h), an actual
transition (from differs from to), rollback default false, envelope size
cap, and a full self-verification round-trip through
`releaseverify.NewVerifier(...).Verify(...)` with a host-shaped context.

## Tests

```sh
GOWORK=off go test -race -shuffle=on -count=1 ./...
GOWORK=off go vet ./...
```

The suite proves byte-canonical encoding (envelope and payload re-marshal to
identical bytes, no trailing newline), independent verifier acceptance of
the produced artifact, key-leak-free errors, fail-closed rejections for
every invalid input class, atomic output (failed sign never creates or
modifies the output file), release-ID derivation, keygen pair consistency,
and the canonical-JSON ledger reader.
