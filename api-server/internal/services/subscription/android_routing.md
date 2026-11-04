# Android routing generator

```go
func GenerateAndroidRouting(profile RoutingProfile, providers map[string][]string, nodes []NodeInfo) ([]byte, error)
```

The result is JSON with `version: 1`, ordered `rules`, and `autoTestUrl`.
The caller must separately generate VLESS links and append a
`# proxima-routing-rules: <base64(JSON)>` line to profile content. The generator
does not fetch providers, encode base64, or change HTTP/client detection.

Before allocating expanded rules or resolved policies, a preflight budget
limits referenced payload entries to 250,000 (counting every repeated reference),
input nodes to 4,096, and estimated JSON to 32 MiB. The byte estimate includes
worst-case JSON escaping and candidate repetition for every emitted rule, so
it can reject an output whose actual encoding would be smaller. Domain/IP runs
are accounted as grouped arrays for domain/ipcidr providers; every classical
entry is conservatively charged as a separate rule because explicit policies
may split adjacent predicates. Exceeding a limit returns `AndroidRoutingError` with path
`budget` and nil bytes, before payload parsing or JSON allocation.

## Input and expansion

- Pass downloaded, decoded HTTP provider payloads keyed by provider key.
  An absent key is an error; a present empty payload matches nothing.
  Inline providers use their profile payload even if the map has the same key.
- Only `vless_reality` nodes become candidates. Link fragment names must equal
  `NodeInfo.Name`, including spaces and punctuation. Duplicate, blank, DIRECT,
  and REJECT candidate names fail instead of producing ambiguous references.
- `RULE-SET` entries expand at their original position. Domain entries become
  Xray `full:`, `domain:` (Clash `+.`), or unprefixed keyword patterns. IP entries are
  validated concrete IPv4/IPv6 CIDRs. Domain and IP arrays stay in separate
  rules because combining them would make Xray require both conditions.
- Classical payloads support DOMAIN, DOMAIN-SUFFIX, DOMAIN-KEYWORD, IP-CIDR,
  IP-CIDR6, and DST-PORT, as `TYPE,value[,target][,no-resolve]`.
  Only IP entries may include the trailing `no-resolve` option. Explicit
  DIRECT/REJECT targets become direct/block; other targets resolve an exact
  group key or display name (for example PROXY must name an existing group).
  Unknown, empty, or ambiguous targets fail rather than inheriting silently.
  Entries without a target inherit the enclosing RULE-SET target. An explicit
  target does not evaluate or inherit an unused enclosing policy.
  Adjacent entries batch only when their resolved target, policy, candidates,
  and domain/IP dimension agree. Nonadjacent entries are never reordered.
- BlockQUIC emits the first rule with `network: "udp"`, `port: "443"`, and
  `target: "block"` only when the saved profile's BlockQUIC flag is true.
  The final rule has `match: true`.
- Private providers are treated exactly like other providers: supply concrete
  private domain/CIDR entries. No private list is guessed or hardcoded.
  Unexpanded geoip/geosite references, wildcard/leading-dot domains, logical
  rules and other unsupported matches fail the whole
  generation. They never become empty-condition rules or silent omissions.

## Policy assumptions and limits

- `policy` is the resolved group's stable key, an identifier for the Android
  leastPing balancer, not the string `url-test`. Candidates are exact names.
- A select group uses its first live member, the default selection in a fresh
  Clash profile. Persisted client selections are not input to this function.
  DIRECT and REJECT selected branches remain direct/block, without candidates.
- Empty groups are pruned using the same resolver as Clash. An empty rule
  target or final group returns an error rather than silently changing policy.
- Flat url-test and fallback groups map to leastPing candidates. This preserves the
  eligible set, not Clash's tolerance, lazy-probe, or interval behavior; these
  options have no fields in the supplied Android contract.
- Android intentionally replaces ordered failover within a fallback group
  with leastPing across that group's eligible nodes. Candidate array order is
  deterministic but is not a failover priority. The enclosing select still
  picks its first live branch: kr_home candidates are never merged with the
  lower-priority kr_all or jp_auto branches. This priority is determined from
  available subscription nodes, not runtime health-based switching of groups.
  A nested automatic group that would require multiple independent
  selections, or automatic selection mixing proxy and DIRECT/REJECT, fails.
  Single-member groups preserve their selected child policy.
- Active url-test and fallback groups must agree on one URL (the schema has one
  `autoTestUrl`). With no active custom probe URL, the existing subscription
  default `https://cp.cloudflare.com/generate_204` is used.
- The wire schema has no `no-resolve` flag or DNS strategy. IP predicates
  therefore operate on the destination IP supplied by the Android client.
  This generator cannot promise Clash DNS-resolution equivalence; the client
  must own its DNS strategy. Clash DNS/general settings are not translated.

Errors are `*AndroidRoutingError` (possibly wrapped), with `Path` and `Problem`.
Use `errors.As` to inspect them. A failed conversion returns nil bytes, never
partially correct routing metadata. With multiple live `kr-home` nodes, the
default profile emits policy `kr_home` with exactly those candidates; Korean
services use leastPing within that set, while overseas traffic and the default
ads branch retain the separate `jp_auto` policy.
