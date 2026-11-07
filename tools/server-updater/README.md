# Proxima server-updater (first increment)

A host-side, single-purpose update controller for the `api` and `web`
containers of a Proxima deployment. It accepts one narrow request over a
root-owned local Unix socket: apply a previously approved, locally pinned
image pair. It has no other authority.

## What this tool deliberately cannot do

**It cannot enroll an existing deployment by itself, and it cannot apply a
GitHub release automatically. Both are hard refusals in the code, not gaps to
be filled later.** Three independent mechanisms enforce this:

1. **Managed-runtime label.** The controller only operates on `api`/`web`
   containers that carry `io.proxima.updater.runtime` with a value equal to
   the SHA-256 fingerprint of the local `/etc/proxima-updater/config.json`
   (`Docker.verify`, `compose.go`). The current production stack is an
   unmanaged `docker compose` deployment (plus Caddy) that does not carry
   this label, so preflight rejects every apply with **zero downtime**
   (`TestPreflightRejectsUnmanagedOrMissingImagesWithoutDowntime` /
   `unmanaged`). Enrollment is a documented manual maintenance-window
   operation (`INSTALL.md`).
2. **No fetch path.** There is no registry client, no tag resolution, no
   download code and no shell anywhere in this module. Approvals are
   root-written local files with exact `repo@sha256` digests that must already
   be loaded into the local Docker daemon. Every new apply additionally requires
   a fixed local signed manifest and a v2 replay journal (`SIGNED_RELEASES.md`);
   signatures do not enable automatic fetching or enrollment. The fake-executor
   suite fails any subprocess outside the fixed `docker`/`docker compose`
   allowlist (`fakeExecutor.Run` marks every unexpected command unsafe and the
   tests assert `unsafe == false`).
3. **No arbitrary Compose input.** The runtime Compose spec is generated in
   Go from the root-owned config; the protocol rejects commands, paths,
   images, Compose args and unknown fields outright
   (`TestProtocolRejectsPrivilegeExpansion`).

## Security model

- Transport: `/run/proxima-updater/control.sock`, root:\<socket_gid\> 0660,
  directory root:\<socket_gid\> 0750 (checked at startup). No TCP listener exists. Socket group membership is the
  authorization to trigger downtime - treat it like sudo.
- Managed API access: the only host bind is the fixed directory
  `/run/proxima-updater` at the identical container path, read-only with
  `rprivate` propagation and `create_host_path: false`. Binding the directory
  lets clients reconnect to a replacement socket after updater restart;
  the systemd unit preserves that directory across stops. Read-only prevents
  filesystem changes, not socket requests. The API receives the numeric
  `socket_gid` as a supplementary group; web receives neither mount nor group.
  Neither config nor HTTP callers can supply bind paths. No Docker socket,
  updater config, approval or state directory is exposed to either container.
- Preflight checks the exact effective mount set (including duplicate
  destinations), bind source/destination, read-only mode and propagation,
  and supplementary groups. The bind must use the structured Docker Mount
  API produced by the generated Compose spec: inspect `Mode` is empty,
  `RW` is false and `Propagation` is `rprivate`. Legacy string binds are not
  an enrollment substitute. Runtime fingerprint v2 requires manual
  re-enrollment of older managed containers; network, loopback ports and
  both named data volumes remain unchanged.
- Inputs: `/etc/proxima-updater/config.json` and
  `/etc/proxima-updater/approved.json` must be root-owned regular files with
  no group/world write and no symlink in any ancestor (`trustedPath`), strict
  JSON (unknown fields, duplicate keys and trailing data rejected).
- Subprocesses: fixed `/usr/bin/docker` and
  `/usr/local/libexec/docker/cli-plugins/docker-compose` (both verified with
  `trustedPath` at startup), a fixed environment, killed as a process group
  on deadline. Any mutating Compose command failure, timeout or oversized output is classified
  `command outcome uncertain` and never retried blindly.
- State: exclusive `flock` on `/var/lib/proxima-updater/controller.lock`,
  atomically written, fsynced state journal (`state.json`) records the phase
  before each Docker side effect. Crash or ambiguous outcome => persisted
  `manual_recovery` with `maintenance: true`; the controller then refuses
  every new apply until a human resolves it. Deleting the state file is not
  the unlock procedure.
  A separate `recovery-required` marker is fsynced before any Docker mutation
  and survives terminal journal rename/sync failures. It is removed only after
  durable success or rollback; on restart it overrides a terminal journal.
- Recovery containment: after an operation enters manual recovery (including
  journal failure), and before opening the socket on a recovery restart, the
  controller attempts to stop only positively identified managed api/web
  container IDs. Cleanup has a separate 100-second budget even on cancellation.
  Failed shutdown verification is reported as `recovery_stop_unverified`.
  The gate remains closed even after successful stop verification: a delayed
  Docker create/start request can still complete after that snapshot.
- Downtime: apply stops `web` and `api` explicitly (never `db`, `redis` or
  Caddy), starts api then web with readiness checks bound to 127.0.0.1
  (no redirects, no proxy), then re-verifies the exact container image IDs.
- Rollback: only a recognized readiness failure after successful Docker commands
  allows the controller to roll back
  to the exact prior image pair **only** when the approval says
  `rollback_compatible: true`. The API runs schema migrations at startup that
  are not globally atomic and `/ready` checks connectivity but not migration compatibility, so `migration_uncertain`
  is persisted true for every apply, and an incompatible or uncertain failure
  is `manual_recovery` - never an automatic DB-touching rollback.

## Layout

- `main.go` - startup, trusted-path checks, signal handling
- `config.go` - strict JSON + trusted-path validation of config/approval
- `compose.go` - generated runtime Compose spec (JSON), config fingerprint
- `docker.go` - preflight, container identity verification, stop/up
- `executor.go` - bounded, allowlisted subprocess execution
- `readiness.go` - loopback HTTP readiness probes
- `controller.go` - the operation state machine
- `server.go` - Unix-socket HTTP transport
- `store.go` - persisted state + exclusive lock
- `proxima-updater.service` - hardened systemd unit

See `INSTALL.md` for prerequisites, enrollment and preapproval, and
`PROTOCOL.md` for the exact wire protocol intended for the future parent-API
integration.

## Tests

```
cd tools/server-updater
GOWORK=off go test -race -shuffle=on -count=1 ./...
```

All Docker interaction goes through `fakeExecutor`, so the suite is
deterministic and never touches a Docker daemon or the host. `f.unsafe` fails
the run if any test path reaches a command outside the fixed allowlist.
