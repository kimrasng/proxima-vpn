# Direct VLESS Reality E2E

Runs the real panel API, PostgreSQL, Redis, an exit-only `node-agent` with
Xray, and a separate Xray client in isolated Docker networks. The admin API
registers one exit, publishes its direct chain to a subscription group, and
configures VLESS Reality. A user device obtains the generated subscription.
The client uses its UUID and Reality credentials to dial the exit IP directly,
retrieves a local HTTP marker, then proves a different UUID cannot retrieve it.

No entry relay, public DNS, ACME, or production database is involved. The
Reality decoy is local and self-signed; the shared relay-E2E image wrapper
allows only the local HTTP marker destination for this test.

```bash
bash tests/direct-e2e/run.sh
```

The runner uses an isolated Compose project and removes its containers,
networks, volumes, and test image on exit. Docker needs privileged Linux
containers and nftables support.

## Account cap enforcement stage

After the baseline checks, `run.sh` also exercises the per-account concurrent
client-address cap end to end. One account (cap 1) connects from two different
source addresses with the **same shared UUID**: the first address opens a tunnel
and keeps it open, then the second address connects. The stage asserts that

- both tunnels worked before enforcement acted,
- the panel then cut the **newer** address off,
- the **older** address kept its tunnel, and
- the shared UUID still works for a fresh connection (the cap closes transports;
  it never revokes the credential).

It needs a kernel with `CONFIG_INET_DIAG_DESTROY` and `CAP_NET_ADMIN` on the
Exit, and it waits for the scheduler (about 75 s). Whether the host can do this
is decided by the Exit agent's own startup probe, which really destroys a
socket, so the stage is skipped with a visible `SKIP:` line on kernels without
it (Docker Desktop). `CAP_E2E=1` forces it, `CAP_E2E=0` disables it, and
`KEEP_E2E=1` leaves the stack running for debugging.

The held tunnel is an idle stream to a silent listener, not a slow download:
the Exit serializes download chunks per UUID, so a slow transfer would stall the
other client for a reason unrelated to the cap.

