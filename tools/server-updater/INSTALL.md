# Installation and preapproval

The controller runs on the Docker host as root. It never modifies anything
outside `/etc/proxima-updater`, `/var/lib/proxima-updater`,
`/run/proxima-updater` and the `api`/`web` containers.

## Hard prerequisites (checked at startup, fail closed)

| Requirement | Why |
|---|---|
| Linux, running as root | socket ownership, `flock`, docker control |
| `/usr/bin/docker` present, root-owned, no writable ancestor, not a symlink | fixed binary |
| Docker Compose plugin at `/usr/local/libexec/docker/cli-plugins/docker-compose` (root 0755; copy or symlink-free install of the plugin binary there) | docker CLI finds plugins under `/usr/local/libexec/docker/cli-plugins` by default; the updater refuses any other location |
| `/var/lib/proxima-updater` 0700 root, containing `docker-config/` (0700 root) | state dir; `DOCKER_CONFIG` target so no user config leaks in |
| `/run/proxima-updater` 0750 root, group equal to numeric `socket_gid`, no symlink | checked at startup; systemd `RuntimeDirectory` provides this |
| A dedicated group for the parent API, e.g. `proxima-updater` | socket authorization = downtime authority; `Group=proxima-updater` must resolve to `socket_gid` |
| api/web already running under the **managed runtime** (see enrollment) | preflight rejects anything else |
| Both target images already `docker pull`ed **by digest** into the local daemon | the updater never pulls, builds or fetches |

## Install steps (as root, on the host)

1. Build and copy the binary:
   from `tools/server-updater`, run `GOWORK=off GOOS=linux GOARCH=amd64 go build -o /tmp/proxima-updater .`
   (use `GOARCH=arm64` for an ARM64 host),
   then install to `/usr/local/sbin/proxima-updater` (root 0755).
2. Install the compose plugin at the pinned path (see table above).
3. `groupadd -r proxima-updater` (note the numeric GID for the config).
4. `mkdir -p /etc/proxima-updater /var/lib/proxima-updater/docker-config`,
   both root 0700 (docker-config 0700).
5. Write `/etc/proxima-updater/config.json` (root 0600), strict JSON:

```json
{
  "project": "proxima",
  "network": "proxima_default",
  "app_volume": "proxima_app_data",
  "uploads_volume": "proxima_uploads_data",
  "api_environment": {
    "DATABASE_URL": "postgres://...@db:5432/vpnpanel",
    "REDIS_URL": "redis://redis:6379",
    "JWT_SECRET": "...",
    "PANEL_URL": "https://panel.example.com",
    "ADMIN_EMAIL": "...",
    "STRIPE_SECRET_KEY": "...",
    "STRIPE_WEBHOOK_SECRET": "..."
  },
  "socket_gid": 999
}
```

`project` **must equal the existing deployment's Compose project name**
(the `com.docker.compose.project` label on the current api/db/redis
containers, from `docker inspect` or `docker compose ls`), and `network` the
existing Compose network name (typically `<project>_default`), so db/redis
keep serving the same DNS names and Caddy keeps resolving `api`/`web`. The
volumes must be the existing named volumes (e.g. `<project>_app_data`,
`<project>_uploads_data`); the updater refuses to create replacements.

6. Copy `proxima-updater.service` to `/etc/systemd/system/proxima-updater.service`,
   `systemctl daemon-reload && systemctl enable --now proxima-updater`.
7. Verify status:
   `curl --unix-socket /run/proxima-updater/control.sock http://localhost/v1/status`

## API socket access and host provisioning

The generated runtime grants only the API container a fixed read-only bind:
`/run/proxima-updater` on the host to `/run/proxima-updater` in the container.
It adds the configured numeric `socket_gid` via Compose `group_add`. The
socket remains `control.sock`, root:`socket_gid` 0660. The enclosing directory
must be root:`socket_gid` 0750; a mismatch with the systemd group's GID now
prevents updater startup. Neither caller requests nor config can choose a
different bind source, destination or mode. The Docker socket is never mounted.

The API image's existing UID and primary GID are retained, not overridden.
When `api_environment.PROXIMA_RELEASE_WATCH_DIR` is configured, its only
accepted value is `/var/lib/proxima-release-watch`. The generated runtime
preserves that exact writable directory bind, with `rprivate` propagation and
`create_host_path: false`. Provision the directory root-owned 0700 before
enrollment. No configurable host path is accepted, and web receives no watch
mount. Preflight rejects a missing, read-only, redirected or additional bind.

The current API Dockerfile has no `USER`, so it runs as container root; this
change does not make it nonroot. An approved nonroot image can use the numeric
supplementary group without defining a matching group name in `/etc/group`,
provided its entrypoint does not drop that group. Review any nonroot image's
write access to the existing app/uploads volumes separately; the updater does
not chown volumes. Host provisioning assumes rootful Linux Docker without
user-namespace remapping. Rootless/remapped IDs need an explicitly reviewed
host/container GID mapping before enrollment; do not fix access by making the
directory or socket world-writable. Socket access grants permission to apply
root-approved releases, including downtime, even through a read-only bind.

The directory must exist before Compose creates the API. Long bind syntax uses
`create_host_path: false`, so Docker cannot silently create an unprovisioned
directory; propagation is `rprivate`. Use the generated runtime, not a hand-made
short bind: preflight expects Docker's structured Mount API representation
(`Type=bind`, exact `Source` and `Destination`, empty `Mode`, `RW=false`,
`Propagation=rprivate`). Extra mounts, duplicate destinations, a socket-file
bind, write access or a missing/wrong supplementary group are rejected before
services stop. This checks effective mount configuration, not every historical
creation-time option or live connectivity to the socket.

`RuntimeDirectoryPreserve=yes` keeps the directory inode stable across updater
stop/start; the daemon replaces only the socket file and API clients reconnect
to it. Do not remove/recreate the directory while a container is bound to it:
that leaves the container attached to a stale inode. Recreate the API container
from the generated runtime if this happens. `/run` is lost on host reboot;
provision the directory before Docker restores the managed containers (for
example, a root-owned `/etc/tmpfiles.d/proxima-updater.conf` entry
`d /run/proxima-updater 0750 root proxima-updater -`, applied by
`systemd-tmpfiles-setup.service` before `docker.service`). Ensure the host's unit
ordering actually guarantees this; `After=docker.service` on the updater alone
does not order Docker's container restart policy after directory provisioning.
Start the updater and verify socket connectivity from the actual API process
identity before enabling parent-admin control. A host-root curl or successful
inspect preflight does not prove a nonroot container can connect.

Existing managed v1 containers must be manually recreated using the v2 rendered
bootstrap during enrollment. Restarting containers alone cannot add a mount or
supplementary group, and the old runtime fingerprint fails preflight. Keep the
existing named volumes, shared network and loopback ports in the rendered file.

## Enrollment (manual maintenance window - cannot be automated)

Today production is an **unmanaged** Compose stack with Caddy in front. The
controller will refuse every apply until the api/web pair runs under the
managed runtime, and it will never restructure a live deployment by itself.
An operator performs enrollment during a maintenance window:

0. **Caddy decision first.** The managed runtime publishes api/web only on
   `127.0.0.1` (2053/8080) and joins them to the existing Compose network
   with their service aliases. Check how Caddy reaches the services today:
   if it proxies to `api:2053` / `web:8080` over the shared network,
   enrollment changes nothing for it; if anything else (host firewall, a
   non-Docker reverse proxy, direct external access to 2053/8080) depends on
   the current published ports, that dependency breaks on purpose - fix that
   dependency consciously before continuing. **Direct external access to
   2053/8080 does not survive enrollment.**
1. Write the approval file for the enrollment pair: `from` = `to` = the exact
   digests currently running (`docker inspect --format '{{.Config.Image}}'`
   on api and web - refuse to continue if these are not `@sha256:` pinned;
   resolve and verify the current image digest before recreating the deployment
   pinned during maintenance; merely adding a tag does not change Config.Image).
   `/etc/proxima-updater/approved.json` (root 0600):

```json
{
  "id": "enroll-2026-11-04",
  "from": {"api": "ghcr.io/.../api@sha256:...", "web": "ghcr.io/.../web@sha256:..."},
  "to":   {"api": "ghcr.io/.../api@sha256:...", "web": "ghcr.io/.../web@sha256:..."},
  "rollback_compatible": true
}
```

2. Render the enrollment runtime:
   `/usr/local/sbin/proxima-updater --render-bootstrap > /var/lib/proxima-updater/bootstrap.json`
   This command only prints JSON. Its enrollment validation requires equal
   `from`/`to` pairs with valid pinned digests. It does not authorize an update:
   `/v1/apply` continues to reject equal pairs. `/v1/preflight` accepts this
   enrollment file for read-only verification of the running deployment.
3. Stop the unmanaged pair, recreate under the same project name:
   `docker compose -p <project> --project-directory /var/lib/proxima-updater --env-file /dev/null -f /var/lib/proxima-updater/bootstrap.json up -d --no-deps --no-build --pull never --force-recreate api web`
   (run it with the same fixed environment discipline the updater uses, or
   from a shell with no `DOCKER_*`/`COMPOSE_*` variables set).
   db/redis/Caddy are not in the file and are not touched. Do not use
   `--remove-orphans` - it would delete db/redis.
4. With the equal-pair enrollment file still installed, run
   `curl --unix-socket /run/proxima-updater/control.sock http://localhost/v1/preflight`
   must answer `{"ok":true,"reason":""}`. If it says
   `topology_mismatch`, the running pair still is not the managed runtime -
   nothing has been applied, nothing further will run.
   This verifies pinned images, managed labels, mounts, network and loopback
   bindings without changing containers or consuming approval. Check HTTP
   readiness and the actual Caddy route separately: preflight does not probe
   HTTP. A successful enrollment preflight is not permission to update.
   For a real update, replace the enrollment file using the preapproval
   checklist below; at least one service image must change.

**Never re-run the old `docker-compose.prod.yml` against api/web after
enrollment**: it recreates them without the managed runtime label and the
controller then refuses every apply (fail closed) until re-enrollment.

## Preapproval checklist for every later release

1. Fetch and verify the release images **outside this tool**
   (`docker pull repo@sha256:...`), including their provenance - the updater
   only checks that the exact digests exist locally; it cannot judge their
   content.
2. Write `approved.json` (root 0600) with `from` = the digests currently
   running (must match byte-for-byte - preflight verifies it), `to` = the new
   pair (distinct from `from`), and `rollback_compatible`. The equal-pair
   enrollment file cannot serve as an update approval.
3. Decide `rollback_compatible` honestly: the new api **runs schema
    migrations at startup that are not globally atomic**, `/ready` checks
    connectivity but not schema compatibility, and partial migrations may
   already have committed when a failure
   is detected. `true` asserts the operator has reviewed that the target
   schema and its partial states remain compatible with the old image pair
   (including shared `uploads_data` writes). If not reviewed with that level
   of care, write `false` - the controller then enters `manual_recovery` and
   attempts to stop the managed services instead of guessing.
4. Trigger: `curl --unix-socket /run/proxima-updater/control.sock
   -H 'Content-Type: application/json' -d
   '{"release_id":"<id>","maintenance":true}' http://localhost/v1/apply`
   and poll `/v1/status`.

## Manual recovery

When status shows `manual_recovery` (crash, timeout, stop failure,
incompatible failure): the state file is the audit record - do not delete it.
The controller attempts to stop the identified managed pair, including on
restart, with `maintenance: true`. This flag is a recovery gate, not proof that
services are down: Docker can be unavailable, identity can be ambiguous, or a
timed-out create/start request can finish after the stopped snapshot. A failed
stop verification reports `recovery_stop_unverified`; even a verified stop does
not clear the gate. Recovery is deliberately human: settle outstanding Docker
operations and reconcile the actual containers with `docker inspect`,
decide whether the database received partial migrations, and either
re-enroll/raise the pair by hand with `docker compose ... up -d --no-deps
--pull never --force-recreate api web` against a runtime.json you have
reviewed, or roll back by hand. Only after the deployment is verified
healthy should root reconcile `state.json` while the daemon is stopped, preserving
the high-water and all consumed IDs, accepted payload identities and approval
snapshots. Do not archive, delete or reset this journal. Follow the explicit v2
enrollment/reconciliation procedure in `SIGNED_RELEASES.md`, including fixing
the cause recorded in `error` before clearing the operation.
After persisting and syncing the reconciled journal, root removes the
`recovery-required` marker and syncs the state directory before restarting the
daemon. Leaving the marker present forces recovery again. Never remove it merely
because `state.json` says success: it may record an uncertain terminal write.

## Remaining blockers / non-goals of this increment

- **Signed enrollment is required.** Every new apply requires the fixed local
  signed envelope and root approval, plus an explicitly provisioned v2 journal.
  Missing/legacy state cannot authorize updates. See `SIGNED_RELEASES.md` for
  key provisioning, baseline migration and replay-preserving recovery.
- **Parent API authorization remains separate.** This module supplies the
  socket directory mount and group mapping, not the admin-facing HTTP routes.
  The parent must enforce admin authorization and explicit downtime consent
  before sending requests described in `PROTOCOL.md`.
- **Caddy stays unmanaged.** The updater never starts, stops or upgrades
  Caddy, db or redis, and never recreates their volumes.
- **Single host, single instance, no concurrent Docker operators.** The flock
  serializes controllers, not humans; if another operator runs Compose
  concurrently, preflight/verification fails closed.
- **Trusted-path checks require uid 0** (`trustedPath`); the unit tests
  cannot exercise that branch on a non-root dev machine - it is enforced at
  runtime on the Linux host.
