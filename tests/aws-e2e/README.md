# AWS multi-VM real-node E2E

Six EC2 VMs (panel, exit1, exit2, entry, client1, client2) in one throwaway VPC in `ap-northeast-2`.
Each VM has its own Elastic IP, so the two clients present distinct public source IPs.
SSH is allowed only from the operator's /32. The panel API is reachable only inside the VPC.

```bash
AWS_E2E_APPROVE=yes bash tests/aws-e2e/run.sh
```

This creates billable resources; estimate cost with https://calculator.aws/.
`run.sh` destroys everything on exit, including failures. If destroy fails it prints the manual command.
Keys, Terraform state and evidence stay under `tests/aws-e2e/.state` and `evidence/` (git-ignored).

## Tests

1. Both exits register through `scripts/install.sh`, come online, and enforcement defaults to on.
2. Direct and entry -> exit relay connectivity, with the egress source checked, plus an unknown-UUID refusal.
3. UUID eviction across two exits: the newer UUID is cut off, the older keeps its tunnel, both exits acknowledge.
4. Client-address cap: one UUID from two client VMs with different public IPs; the newer address is cut off.
5. Stuck pending eviction: exit2 cannot acknowledge, so the eviction stays pending and retry forges no acknowledgment (the known gap).

Scheduler timing makes a full run take roughly 15 to 25 minutes. Evidence is written under `evidence/`.

## Status

Written and statically checked only (`bash -n`, `terraform validate`). Nothing has run against AWS yet, so expect first-run fixes.
It is manual by design: no GitHub Actions workflow references this directory.
