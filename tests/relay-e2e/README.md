# Two-Exit relay topology E2E

`bash tests/relay-e2e/run.sh` requires Docker Compose with privileged Linux
containers and nftables. `bash tests/relay-e2e/run.sh --output NEW_DIR` writes
the same sanitized evidence to a previously nonexistent directory. A successful
run retains `summary.json`, `assertions.jsonl`, `topology.json`, and three nft
policy snapshots. Failed runs retain nothing; raw container logs and credentials
are never copied into evidence. The JSONL checker rejects missing, repeated,
failed, skipped, malformed, or incomplete assertions and sensitive fields.

The disposable database has one locally stored managed hostname
`entry.relay-e2e.test`, resolved by a Docker network alias, and two API-created
explicit `entry_node_id` chains at TCP 24443 and 24444. Independent Exit agents
listen on 8443 with `publish_direct=false`, distinct access groups, users,
devices, HTTP/UDP markers, and non-default Reality SNI
`decoy.relay-e2e.test`. The harness waits for subscription publication (which
requires a fresh config-hash acknowledgement by the running Exit agent), then
compares each subscription's host, port, UUID, public key, short ID, and SNI
against its generated Exit config without retaining these values. Entry forwards
TCP only: it has no Xray process or Exit/client credential volume. UDP markers
travel through Xray SOCKS5 UDP ASSOCIATE over VLESS TCP, not native UDP DNAT.

The live checks include correct TCP and UDP marker/nonce delivery on each Exit;
unused and swapped ports; complete and UUID-only credential swaps; underlay
reachability with direct Reality denial on both Exits; and healthy controls
around negative probes. For backward compatibility only, a **DB-seeded legacy
pool chain** is inserted in the disposable database: the current API rejects
new pool chains. The admin API must return 409 when its referenced pool is
emptied; a DB-only empty-membership fault then causes policy reconciliation,
preserves the protected Exit drop, blocks a fresh legacy tunnel, and recovers
after restoration. Separately, a disconnected control-plane network leaves
the Entry's last-known-good nft table and fresh traffic intact; a deliberately
invalid nft transaction must leave the table intact too.

No public DNS, Cloudflare API, ACME or internet target participates at runtime.
The Reality decoy is a local self-signed OpenSSL server. Building the isolated
image **does** download the pinned Xray release and base/package images. A
test-image-only Xray wrapper permits only loopback marker ports 9090/9091 in
the Exit freedom outbound; production config is not changed. All Compose
resources and the uniquely tagged test image are removed on exit.
