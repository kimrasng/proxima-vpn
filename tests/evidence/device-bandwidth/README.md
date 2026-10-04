# Per-device bandwidth verification evidence

## Actual Xray datapath

Executed the isolated `tests/device-bandwidth/run.sh` harness with Xray **26.3.27**, Go 1.25 and the production `services.WithDeviceEgressRouting` transformer plus the production node-local limiter. No live application or existing customer data was used.

Five scenarios passed with no skips:

- Vision TCP: two device identities, four simultaneous sessions per device across two Exit limiter instances, sharing one fake central budget per device/direction.
- SOCKS UDP payload accounting and independent directional budgets.
- Existing-session closure on permit error, credential removal/rotation and central revocation.
- Limiter shutdown does not fall back to unrestricted Freedom routing.
- Actual Vision/XUDP UDP echo through the generated SOCKS path.

Eight-second measured downloads:

| Device | Configured payload budget | Sessions / Exits | Received payload | Allowed ceiling including burst |
|---|---:|---:|---:|---:|
| A | 131,072 bytes/s | 4 / 2 | 1,048,888 bytes | 1,179,685 bytes |
| B | 262,144 bytes/s | 4 / 2 | 2,113,848 bytes | 2,228,311 bytes |

These rates are synthetic verification values, not product Mbps promises. The harness uses a fake **shared central** authority to isolate the datapath; real Redis atomicity and real DB/API authorization were separately checked below. It does not benchmark full-API latency/capacity, actual installed fleet behavior or 100/1000-Mbps delivery.

Sanitized logs/measurements: `xray-sanitized.json`.

## Actual PostgreSQL and Redis

An independent verifier used disposable PostgreSQL **17.11** and Redis **7.4.2**. Mandatory selected scenarios executed with zero skips:

- Simultaneous requests through two Exit instances consume one atomic device budget.
- Device/direction independence, burst/refill behavior and rate-change handling.
- Plan downgrade and user plan transfer cannot be overwritten by delayed high-rate authorization, including assignment waits and fresh re-reading.
- Authenticated HTTP authorization rejects wrong node, relay role, wrong group, unknown/inactive/expired/quota-exhausted/evicted devices; client-supplied speed cannot override the plan. Redis absence fails closed for limited traffic.
- Production Xray generator and structure digest, legacy/explicit compatibility-port overlap, incompatible collision rejection.
- Direct and relay limited subscriptions require compatible device-egress acknowledgment and withhold failed state.
- Current schema migration applied twice successfully.

One fixture violating the single-protocol node index and one raw-subscription branch omission were discovered, fixed by their owners, and only those failed checks were rerun. Both passed. Isolated services were removed without touching existing databases.

## Other checks

- Node-local TCP/UDP limiter package race tests passed; affected UDP source-claim regressions passed after review corrections.
- Agent integration/secure-config targeted race tests and Linux/amd64 build passed.
- Permit client/authentication limiter tests passed, including redirect credential protection and invalid-key flooding.
- Web build, locale parity, scoped lint and synthetic device-policy display test passed.

## Not established

- Installed Linux fleet throughput, kernel legacy-qdisc cleanup, production control-plane capacity/latency.
- Equivalent Xray runtime behavior for every historical version allowed by the installer; datapath evidence covers 26.3.27.
- Protection against hostile node-local processes racing wildcard SOCKS UDP association ports. The threat model requires trusted local processes.
- Instantaneous physical-wire rate ceilings or immediate revocation of old non-VLESS sessions.
