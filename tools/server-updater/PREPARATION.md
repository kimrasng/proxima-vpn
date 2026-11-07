# Host-side release preparation

Two preparation surfaces exist and share one verifier and fetch client:

1. The root CLI documented below (`--prepare-release`), which requires the
   daemon to be stopped and an independent `approved.json` transition. This is
   the default and always-available path.
2. The opt-in daemon endpoint `POST /v1/prepare` (see `PROTOCOL.md`), which
   runs the same fixed-repository fetch and pull while the daemon runs, but
   only when root has written `/etc/proxima-updater/authorization-policy.json`
   with `{"mode":"publisher_signed"}`. In that mode the signed manifest itself
   - after full signature, pinned-policy, current-pair, high-water and time
   verification - supplies the transition approval, and no per-release
   `approved.json` is consulted. Automatic rollback additionally requires the
   separate root `allow_rollback` permission in the same policy file.

After root enrollment and independent review of `approved.json`, root may run:

```sh
systemctl stop proxima-updater
/usr/local/sbin/proxima-updater --prepare-release=v1.2.3
systemctl start proxima-updater
```

The version above is illustrative; use a real published tag. Stopping the updater
does not stop the managed application containers. Preparation acquires the same
exclusive `controller.lock` as the daemon, so it fails if the daemon or another
preparation is still running. Keep the existing state/socket directories, Docker
binary, Compose plugin, root configuration and Docker configuration in place.
All existing root ownership, ancestor permission and symlink checks still apply.
Run this from the host, not from the API container or the sandboxed systemd unit;
the daemon unit intentionally cannot write `/etc/proxima-updater`.

The only remote input is a tag matching `v[0-9][A-Za-z0-9._-]{0,126}`. Repository
and asset name are compiled in:

`https://github.com/kimrasng/proxima-vpn/releases/download/<tag>/release-envelope.json`

There is no latest-release resolution, URL option, token option, proxy support,
cookie jar, key discovery or trust-on-first-use. The client permits at most one
redirect, exclusively to HTTPS `release-assets.githubusercontent.com` with a
`/github-production-release-asset/<numeric repository ID>/<asset token>` path.
Explicit ports, credentials, fragments, encoded paths and further redirects are
rejected. Signed CDN queries are preserved and never logged. HTTP requires a 200
response with no content encoding, at most 16 KiB of envelope bytes and 16 KiB of
response headers. The total HTTP deadline is 30 seconds; connect and TLS handshake
limits are 5 seconds each. No application retry occurs. A GitHub delivery change
fails closed and requires a reviewed client update, not a wider URL allowlist.

The command reads the protected journal without recovery writes. Only a valid
v2 enrollment/reconciliation or a completed success/rollback with a known current
pair can prepare. A recovery marker, incomplete/failed operation, full ledger,
policy mismatch, consumed identity or unknown source blocks preparation. The
signature verifier checks the pinned key, channel, repositories, source pair,
sequence floor/high-water and trusted host clock. Independent root approval must
match the signed transition. A tag is a delivery selector, not an authenticated
version assertion: v1 envelopes do not sign the GitHub tag. The signed release ID,
sequence and exact root-approved image pair are the authority.

After verifying the running managed source topology, preparation calls only
`/usr/bin/docker image pull <approved repository@sha256:digest>` for API and web.
The existing executor pins the local Unix Docker socket and protected config,
does not use a shell or inherit Docker host/context variables, limits output to
1 MiB and each command to 90 seconds. The whole preparation is capped at ten
minutes. Docker resolves the signed immutable digest for the host platform; a
digest may name an OCI index or platform manifest. Docker image IDs are local
config identities, not manifest hashes. This relies on the trusted local daemon's
digest verification and registry trust, as does subsequent apply. Neither
platform selection nor image references can come from a socket request.

After pulling, the command checks both target images, managed source topology,
volumes/network, freshness and unchanged root policy/approval. It installs the
exact verified canonical bytes as `release.json` using a root 0600 temporary file,
file fsync, atomic rename and parent-directory fsync. A pre-install failure leaves
the prior envelope intact. A directory-fsync failure after rename has an uncertain
durability outcome and prints no successful candidate. Cached images may remain
after any failure; preparation never starts/stops containers or writes the replay
journal. Root provisioning and unrelated Docker administration must not race it.

Success prints JSON containing `status: "prepared"`, `release_id`, `sequence`,
`payload_sha256`, exact `to` digests and `expires_at`. This is a CLI receipt, not a
durable authorization or a claim that apply will succeed later. Restart the daemon
and use `GET /v1/candidate` (owner API: `GET /api/admin/system-updates/candidate`)
to discover the currently verified prepared release without copying its ID from
this receipt. Use the existing apply protocol with that ID and explicit maintenance consent.
Apply reloads trust/approval/envelope, verifies freshness/current/high-water and
durably consumes acceptance. Re-preparing an unconsumed candidate is permitted.
Preparation does not reserve its sequence or reset any ledger history.

## External prerequisites and verification limits

- Independently authenticated signing public key, root policy/approval and a
  reconciled v2 journal with a trusted sequence baseline and current digest pair.
- A trustworthy host clock and root-controlled CA trust, DNS, Docker daemon,
  protected Docker credentials/config and executable/plugin installation.
- Public GitHub release-asset access with the documented one-hop delivery shape;
  private GitHub assets are unsupported. Registry credentials, if required, must
  already be provisioned in `/var/lib/proxima-updater/docker-config` by root.
- A publisher must publish a valid envelope for this exact source transition.
  The current repository workflow's `signing-readiness` job intentionally fails
  until external signing controls are proven. This command does not enable that
  job, provision signing secrets, create approval or bypass the publishing gate.
- Signed/root-reviewed database compatibility and source/target `/ready` support
  remain prerequisites for apply and any permitted rollback.

Tests exercise real local TLS HTTP parsing, redirect rejection, bounds and
cancellation, real Ed25519 verification and the preparation boundary against the
existing simulated Docker runtime. They do not prove actual GitHub delivery,
registry pulls, uid-0 Linux filesystem behavior or deployment. No production
connection is needed for the test suite:

```sh
cd tools/server-updater
GOWORK=off go test -race -shuffle=on -count=1 ./...
```
