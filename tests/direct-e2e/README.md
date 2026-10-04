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
