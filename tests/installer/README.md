# Installer regression tests

Run from the repository root:

```sh
bash -n scripts/install.sh
python3 -m unittest discover -s tests/installer -v
```

Requirements: Python 3, Bash, jq, curl, and the usual shell file utilities
(including `sort -V`, as required by the installer).

The tests execute a **temporary, path-rewritten copy** of the installer. All
installation paths and temporary files stay under a temporary directory; the
root check is bypassed only in that copy. The subprocess receives an allowlisted
PATH, with package managers, curl, uname, node-agent, unzip, systemctl, and firewall
commands stubbed. One regression test permits real curl only for role requests to
local loopback HTTP servers: it verifies that redirect destinations receive no
request or node credential, including when a local curlrc enables redirects.
No external network requests, host package changes, host service changes, or host
firewall changes are performed. Do not run the real installer to test it, and do
not use sudo for these tests.

Coverage includes:

- Relay common dependencies on apt/yum/dnf, registration, authenticated role
  lookup, agent systemd setup, and absence of Xray downloads or unzip installation.
- Exit/both Xray installation only after role confirmation, version floor,
  pinned versions, and arm64 downloads.
- Fail-closed behavior for registration failures, invalid credentials, role HTTP
  failures, non-2xx statuses (including redirects with valid role bodies),
  missing/malformed roles, and unknown roles.
- Legacy command arguments and firewall defaults, custom TCP ports/ranges,
  ufw/firewalld syntax, absent firewall warnings, and invalid port rejection.
- Preservation of existing Xray on a relay and the node-agent download fallback.

Role is not persisted in the current agent config: only `node_id`, `api_key`, and
`server_url` are. The installer registers first and then uses those credentials
with `GET /api/v1/nodes/{node_id}/role` (`X-Node-Key`) before deciding whether to
install VPN software. Role lookup accepts only 2xx HTTP responses and never follows
redirects with the node credential; HTTP URLs remain supported for local setups.
Existing generated commands need no new arguments. Panels
without that endpoint fail closed, rather than assuming exit; after registration,
subsequent installation failure may leave an already-registered node and a consumed
one-time token. These tests do not verify a live package manager, panel, systemd,
or forwarding policy.
