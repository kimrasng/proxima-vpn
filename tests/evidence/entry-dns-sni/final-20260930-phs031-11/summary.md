# PHS-031 final regression gate

Status: PASS

Relay: 24/24 assertions; exit 0.
api: 732 test/subtest passes, 0 skips, 9/15 package passes.
pkg: 97 test/subtest passes, 0 skips, 6/7 package passes.
node: 96 test/subtest passes, 1 skips, 8/10 package passes; nft 1 pass.
Browser: 115/115 passes; 0 skips; 8/8 spec files.

Fixture boundaries: local DNS/DB fixture and syntactically valid test-only managed DNS; no live Cloudflare. Legacy pool compatibility is DB-seeded in the isolated relay harness.

Command exit codes:
- relay: 0
- relay-check: 0
- migration: 0
- api-list: 0
- api-race-tests: 0
- api-check: 0
- api-vet: 0
- api-build: 0
- pkg-list: 0
- pkg-race-tests: 0
- pkg-check: 0
- pkg-vet: 0
- pkg-build: 0
- node-list: 0
- node-race-tests: 0
- node-nft: 0
- node-check: 0
- node-vet: 0
- node-build: 0
- api-binary: 0
- real-admin-api-ready: 0
- order-fixture: 0
- locales: 0
- lint: 0
- web-build: 0
- preview-ready: 0
- browser-real-admin: 0
- real-node-chains-api-ready: 0
- browser-real-node-chains: 0
- real-admin-subscription-domains-api-ready: 0
- browser-real-admin-subscription-domains: 0
- real-user-portal-api-ready: 0
- browser-real-user-portal: 0
- real-user-subscription-domains-api-ready: 0
- browser-real-user-subscription-domains: 0
- browser-mock: 0
- browser-check: 0
- diff-check: 0
- graph-validate: 0
- credential-scan: 0
