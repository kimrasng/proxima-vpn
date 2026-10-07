# Railway free VM deployed-process preflight (2026-10-05)

Source: committed main 74bc476, followed by a local patch to the authenticated high-frequency route guard. No production credentials, databases or .env files were transferred; source transfer used git archive HEAD.

## Environment verified

- Linux x86_64 VM, root, approximately 2 GB RAM.
- Docker daemon functional; privileged isolated container launched successfully.
- nftables table creation/deletion and IPv4 forwarding available.
- Installed Docker Compose v2.39.4 and Buildx v0.28.0 in the disposable VM. Initial legacy Docker build failed because TARGETARCH was unset without Buildx.
- Actual API, PostgreSQL, Redis, Entry agent, two independent Exit agents and Xray 26.7.28 reached healthy state.

## Result: NOT PASS

The existing relay E2E stopped at its first positive TCP marker check (`healthy control A failed`). No full E2E assertions or sanitized success bundle were produced.

Two blockers identified:

1. Exit agents poll revoked-devices every 500 ms, exceeding the global 100 requests/minute/IP limiter. Live Exit logs showed repeated revocation HTTP 429 followed by other control-plane HTTP 429 responses. A narrow local patch extends the existing authenticated permit guard to GET revoked-devices; route matching/authentication tests pass. On rerun the sampled Exit logs no longer showed the previous 429 errors. This is not a long-duration/load proof.
2. The client requests 127.0.0.1:9090, and live Xray logs show routing into the new device-egress SOCKS outbound. The harness only patches Freedom destination rules; node-local device-egress rejects local/private destinations. The loopback marker itself is healthy. A test-only injected destination/dial fixture is required to exercise this datapath without weakening production SSRF restrictions.

## Not established

- Full passing relay E2E on Railway.
- Two-Exit deployed UUID-slot admission, slot release, or revocation ACK integration.
- Cross-VM public network routing or production throughput/capacity.

All test Compose resources from diagnostic runs were removed. No production stack was changed. VM claim/preview links and raw credential-bearing config/logs are deliberately excluded from this evidence.
