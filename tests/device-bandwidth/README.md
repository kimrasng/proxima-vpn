# Device bandwidth isolated verification

This opt-in test package never boots the API server/node-agent, reads `.env`,
connects to an existing database/Redis, or provisions a server. Every payload,
Reality camouflage target, Xray inbound and limiter listener uses loopback.
Documentation-only destinations (`198.51.100.10`) are mapped to the local payload
servers **only through the limiter's injected dial seam**. Production destination
validation is not disabled or changed; local/private egress remains denied there.

The package is intentionally not added to `go.work`. Its runner uses `GOWORK=off`
and local module replacements once the implementations are available.

## Run after the implementations are ready

Supply an isolated executable matching the host architecture (macOS arm64 and
Linux are supported by upstream Xray), plus Go 1.25 or later:

```sh
BANDWIDTH_IMPLEMENTATIONS_READY=1 \
BANDWIDTH_GO=/absolute/path/to/go \
BANDWIDTH_XRAY=/absolute/path/to/xray \
bash tests/device-bandwidth/run.sh
```

The runner does not download/install global packages. All child processes are
bounded by test deadlines and terminated at cleanup. Xray is first run with
`run -test` against each ephemeral configuration. Config files and ephemeral
Reality/TLS keys are deleted by `testing.T.TempDir`. Sanitized measurement/log
JSON and the Xray version are written into a newly created temporary evidence
directory, printed by the runner. No real UUIDs or credential keys are used.

Tests use a fixed measurement window, not a timing-sensitive short transfer.
Ceilings are `rate × measured duration + explicitly stated burst allowance`.
Throughput must also exceed a lower floor so dead forwarding cannot pass merely
by remaining under the limit. Streams sharing a device UUID are measured as an
aggregate against one mutex-protected per-device, per-direction central bucket:
never one budget per stream, Xray server, or exit. Different devices have separate
buckets. TLS payload production
starts at one common barrier after all handshakes, so pre-window socket buffers
cannot artificially inflate the starting burst.

## Verification boundaries

- A fake *central* permit authority isolates SOCKS/Xray behavior; it does not prove
  Redis atomicity, real API authentication, database device-state handling, or
  node-agent process lifecycle. Real Redis/API checks belong in the implementation
  test package and must be reported separately.
- SOCKS UDP is measured with controlled echo datagrams. A separate real Vision
  client enables `xudpConcurrency: 8` and `xudpProxyUDP443: allow`, using client
  flow `xtls-rprx-vision-udp443` to permit the UDP/443 test destination; its XUDP
  echo must pass independently rather than being inferred from SOCKS UDP.
- Loopback injected dialing proves the datapath without granting production
  permission to contact local/private addresses. It is not an SSRF acceptance
  test for real public Internet addresses.
- No throughput result is a guarantee about production Internet performance,
  deployment behavior, or limiter/API outage propagation outside this process.

The adapter calls the actual `services.WithDeviceEgressRouting` API transformer.
Only the node-local SOCKS declaration password and temporary listener port are
materialized before real Xray `run -test` and datapath execution. No full API/node
application is started. Existing TCP sessions must close on permit API error,
central revocation, credential removal and credential rotation. A Freedom
tripwire (test-only redirect to the working local TLS target) makes silent
fallback after limiter shutdown detectable.
