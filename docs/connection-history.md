# Selective connection history

Detailed history is a separate observation system, not the billing ledger.
Collection defaults to OFF. Enable only explicitly selected nodes through the
administrator Dashboard, Nodes, or NodeDetail controls.

This guide describes the implementation and local installation contract.
Supplier and connected acceptance receipts live under
`.omo/evidence/connection-history/`; an installation command or a successful
build does not by itself qualify every protocol. Deployment is a separate
maintenance operation.

## Runtime installation

Xray and Hysteria require the pinned history-enabled builds in
`runtime-extensions/`. A stock binary is not an equivalent event supplier.
WireGuard uses the agent's local kernel observation adapter.

Build both Linux architectures with the owners' build scripts:

```sh
bash runtime-extensions/xray/build.sh /absolute/xray-output linux amd64
bash runtime-extensions/xray/build.sh /absolute/xray-output linux arm64
bash runtime-extensions/hysteria2/build.sh /absolute/hysteria-output
```

Keep each output's checksum sidecars with its executable. After copying the
outputs to their intended local paths on the node, generate a manifest:

```sh
bash scripts/history-runtime-manifest.sh \
  /absolute/xray-output /absolute/hysteria-output
```

Save that JSON as the node's private runtime manifest. The default path is
`/etc/node-agent/runtime-manifest.json`; `PROXIMA_RUNTIME_MANIFEST` overrides it.
Manifest paths refer to actual local files, not download URLs.

```sh
node-agent install-runtime --runtime xray --manifest /absolute/runtime-manifest.json
node-agent install-runtime --runtime hysteria2 --manifest /absolute/runtime-manifest.json
```

The installer verifies executable bytes, approved source/build identity, and
the executable's `history-capabilities` response before atomic replacement.
A failed check preserves the installed executable; there is no automatic stock
downgrade. Keep the artifact files available for future updates. Installing or
replacing a runtime is distinct from toggling collection, which must not restart
the VPN.

For a rollback, first disable collection and confirm applied OFF; retain the
outbox and allow already-durable records to drain. During the separately
scheduled runtime maintenance, install a previously qualified artifact through
the same CLI using its preserved checksum manifest. The artifact must still
meet the agent's approved pin/build and capability checks. A previous stock
binary is not a valid history rollback target, and an artifact rejected by the
gate must not be copied around it. Recheck supplier status before re-enabling;
a successful file installation alone does not prove a running supplier changed.
Keep the matching agent, manifest, binary, and source receipts together.

## Desired and applied state

The administrator bulk command uses each selected node's expected revision:
`POST /api/v1/admin/nodes/connection-logging`. A conflict changes none of the
selected nodes. Refresh the affected nodes before submitting another command.
An already-matching command does not allocate another revision.

Desired ON is not proof of collection. Check the actual applied revision,
collection epoch, every required supplier, and queue/error status. Missing,
stale, unsupported, or unavailable suppliers must not be treated as collecting.
Entry-only nodes cannot obtain encrypted inner destinations by observation,
and selecting an entry must not silently enable its exit.

OFF stops new detailed observations after the suppliers' applied barriers.
It does not delete stored history, stop billing, or terminate the VPN. Records
already queued continue uploading. The final nonterminal samples use the prior
active revision; the disabled boundary and applied acknowledgment use the new
revision. Re-enabling creates a fresh epoch and fresh observation baselines.

## Reading records

Connections history, UserDetail, and NodeDetail expose administrator-only
history. Filters and opaque cursors are server-side. API timestamps use UTC;
the screen identifies the displayed local timezone.

An authenticated tunnel and its destination flows are distinct entities.
Do not add their counters together. Keep proxy payload and observed inner-IP
bytes in separate measurement layers, and do not equate either with charged
traffic. Aggregate endpoints include only complete observed intervals within
the requested time range; partial intervals remain explicitly identified.

Shared Shadowsocks credentials do not identify an individual account.
Unknown identities, domains, parents, and endpoints remain unknown. WireGuard
sessions describe peer activity, not a client's connect-button timestamp.
UDP inactivity is not proof of a physical disconnect.

When collection starts mid-connection, `partial_start` indicates that the
original start is not known. Disable, producer loss, process crash, queue loss,
and missing terminal evidence must not be read as exact VPN termination.
Inspect the detail's quality, gaps, end reason, and final-byte status.

`collection_stopped` means a matching supplier barrier closed that observation
interval. It is distinct from both an actively observed owner and a proven
connection end. Unknown VPN end times remain empty and nonterminal byte samples
remain nonfinal. Actual terminal records keep their observed end evidence.
NodeDetail's history tab fixes both list and aggregate requests to that node;
UserDetail fixes the account while permitting a node filter.

Successful collection-setting commands and history reads use the existing
administrator activity log. Records identify the actor, target and allowlisted
filter scope; raw destinations, credentials and returned event payloads are
not duplicated into audit metadata.

## Queue health and retention

Agent history state lives below the agent configuration directory in
`connection-history`; supplier sockets live in `history-run`. Keep the durable
directory across agent replacement and upgrades. Do not clear the outbox to
hide delivery errors: it contains replay state and records awaiting receipt.

The agent maintains a separate policy/upload loop. A slow or failing upload
must not prevent applying OFF. Retries preserve the pending batch identity and
bytes, and queue reclamation follows a matching committed or expired receipt.
Inspect node queue bytes, dropped events, supplier status, and error codes
before investigating a stalled revision.

Current limits are 256 MiB per node outbox, 1 MiB and 1,000 events per ingest
request. Overflow or durable-write failure is reported as degraded observation
and explicit loss rather than unlimited growth or fabricated complete history.

Current server policy retains detailed history for 30 days, accepts replay
within a 7-day window, and retains receipts for 38 days. Retention runs at
startup and every six hours. Expired replay receives an expired receipt rather
than recreating deleted history. These implementation values require review
against operational capacity before deployment.

## Verification and incident evidence

Retain the exact runtime executable hashes, source manifest hashes, node
desired/applied revisions, epochs, supplier boots, batch receipts, and bounded
queue/loss counters when investigating a failure. Do not copy authentication
manifests, credentials, or raw destinations into general error or audit logs.

The evidence directory separates supplier qualification, local disk/HTTP
replay, PostgreSQL ingest, fixture-based UI tests, and actual connected/browser
proof. A passing check in one category does not replace another.
