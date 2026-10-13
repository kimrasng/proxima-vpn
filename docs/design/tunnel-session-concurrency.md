# Concurrent authenticated tunnel sessions

Current PHS4 contract, clarified by the user: **count sessions, not addresses
or physical devices**. x-hwid is absent from the system model; it is never
used as a hint, identity or admission input.

A session is one authenticated live inbound tunnel transport owned by an
Exit. Separate transports sharing one account UUID and source address consume
separate slots. Several destination streams inside a single tunnel do not.
Entry NAT attribution may help locate a connection but does not change its
slot identity; an Entry hop is not an additional session.

The previous source-address cohort implementation is not enabled as a
session limiter. Its primitives and tests remain experimental and must not
be used to deduplicate session slots by IP or UUID.

Implemented initial policy tests verify:
- same IP and UUID with different TCP peer ports count separately;
- repeated reports of one exact Exit transport count once;
- transports at different Exits count separately;
- incomplete snapshots, conflicting ownership and tied cutoff times do not
  provide permission to evict an arbitrary session.

Automatic first-contact session admission, exact per-session local egress
credentials and integrated N+1 enforcement remain unfinished. Default runtime
still uses the legacy UUID admission path and must not be advertised as a
session-count limiter.
