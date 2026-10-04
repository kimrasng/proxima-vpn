# Phase 1: Minimal relay-only node installation

Status: Planned; implementation has not started.

## Goal

Fresh nodes provisioned as `relay` (entry servers) install only management and L4 forwarding components. Do not install unused VPN engines merely because the installer is shared with exit nodes.

## Required components

- Node management agent and its systemd service.
- nftables and the minimal networking utilities required by the forwarding implementation.
- IPv4/IPv6 forwarding configuration as required by the existing relay implementation.
- Chain-based forwarding and return-traffic firewall rules.
- CA certificates and minimal download, installation, and management utilities.

## Excluded components for fresh relay-only installations

- Xray-core and other VPN engines.
- VPN inbound and user authentication configuration.
- VPN server certificate issuance and renewal tooling or jobs.

## Implementation scope

1. Resolve the registered node role before installing VPN components; verify the existing registration/install API contract before choosing the mechanism.
2. Separate relay-only prerequisites from VPN setup in the installer.
3. Retain node agent installation, startup, health reporting, and chain-policy synchronization.
4. Preserve existing exit and combined-role installation behavior.
5. Preserve configured-chain forwarding, including speed-plan ports where required. Installing a relay must not invent a forwarding destination; administrators still configure chains.
6. Add focused role-specific installation checks. At minimum, run `bash -n scripts/install.sh`; also verify the role branches and retained prerequisites with targeted tests before declaring completion.

## Acceptance criteria

- A fresh relay-only install does not download/install unused VPN engines or configure VPN-only certificate tooling.
- The node agent starts and can apply configured L4 chain policies.
- Exit and combined-role installations retain their required VPN components.
- Chain forwarding and management remain functional without Xray installed on the relay.

## Out of scope

- Automatically removing software from existing servers.
- Role-change migration.
- Optimizing installation dependencies for every exit VPN protocol.
- Changing unrelated firewall preset semantics.

## Code references

- `scripts/install.sh`
- `api-server/internal/handlers/admin_node.go`
- `node-agent/cmd/main.go`
- `node-agent/internal/relay/relay.go`
- `pkg/nodeprov/nodeprov.go`

## Tracking note

The installed spec-graph CLI requires explicit legacy `PREFIX-NNN` IDs, whereas the available skill requires CLI-generated IDs. Graph registration was blocked by this version mismatch; no graph entities were created. This file preserves the agreed scope until it can be registered with a compatible CLI.
