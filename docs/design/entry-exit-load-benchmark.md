# Entry → Exit load benchmark

`bash tests/relay-e2e/load.sh [--output FILE] [loadgen bench flags]` builds the
relay E2E topology (one Entry forwarding with nftables DNAT, two Exits running
Xray VLESS Reality with device egress, a real control plane) and drives both
routes through real client tunnels. Every byte takes the production path:
client Xray → Entry DNAT → Exit Xray → device egress (central Redis permits) →
target. Nothing outside the disposable Compose project is contacted.

It measures, per route and then with all routes at once:

- single-stream TCP download and upload (64 MiB each);
- first response on a new tunnel connection (Reality handshake through the
  Entry, admission, egress dial, one round trip), for an HTTP opening and for
  an opaque one;
- round-trip time on an established connection;
- paced UDP (2000 × 1200 B at 1000 pps) through SOCKS UDP ASSOCIATE: loss and
  RTT;
- 8 concurrent 16 MiB downloads per route, both routes together;
- the Entry's per-route counters on the panel against the payload that crossed
  each tunnel.

The run fails if a route is below 5 Mbps single-stream, loses more than 5 % of
UDP, a concurrent stream fails, or the Entry counted fewer bytes than the
payload. The thresholds only catch a broken path; the numbers are the result.
The JSON report (`--output`) holds every figure and the configuration.

## Results

Docker Desktop 4.x on an Apple M3 (8 vCPU, 8 GiB to the VM, linuxkit 7.0.12),
all containers on one host. Representative run, 2026-10-12:

| | route A | route B |
|---|---|---|
| TCP download, single stream | 1776 Mbps | 1719 Mbps |
| TCP upload, single stream | 6213 Mbps | 9576 Mbps |
| First response, HTTP opening (p50 / p95) | 3.6 / 6.8 ms | 2.8 / 3.7 ms |
| First response, opaque opening (p50 / p95) | 215 / 231 ms | 216 / 231 ms |
| RTT, established (p50 / p95) | 0.16 / 0.18 ms | 0.12 / 0.44 ms |
| UDP loss at 1000 pps | 0 % | 0 % |
| UDP RTT (p50 / p95) | 0.20 / 0.32 ms | 0.21 / 0.53 ms |
| Entry counted ÷ tunnel payload (up / down) | 1.022 / 1.015 | 1.018 / 1.011 |

Concurrent: 16 / 16 streams × 16 MiB in 0.61 s, 3.5 Gbps aggregate download.
Across four runs the aggregate ranged 3.5–5.5 Gbps and single-stream download
1.7–1.9 Gbps; treat differences under ~30 % as noise.

## What this does and does not show

- The forwarding path, admission and per-device permit loop sustain multiple
  Gbps on one host without loss. With every hop on the same machine this is a
  CPU-bound software ceiling, not a link or fleet capacity figure; it says
  nothing about WAN latency, loss or real NIC limits.
- Entry route counters are accurate: they always cover the payload and exceed
  it by 1–2 % (TLS/Reality, VLESS, TCP/IP headers).
- **Opaque first bytes cost ~215 ms per new connection.** The Exit's Reality
  inbounds enable Xray sniffing (`destOverride: ["http", "tls"]`). When a
  connection's first bytes are neither HTTP nor TLS - and for server-speaks-first
  protocols such as SSH, SMTP or many game protocols - Xray holds the
  connection until its sniffing timeout before dialling out. HTTPS and HTTP
  traffic is unaffected (3–7 ms). This is existing inbound configuration, not
  the relay path; changing it (`routeOnly`, or narrowing sniffing) is a
  separate decision.
- Upload is faster than download here. Both directions take the same permit
  path, so the asymmetry is most likely CPU scheduling on the shared host; it
  is recorded, not explained.

## Running against other hosts

`load.sh` only builds its own disposable topology. Pointing `loadgen bench`
at real nodes needs an approved test plan: it generates sustained multi-Gbps
traffic that counts against a real device's bandwidth budget.
