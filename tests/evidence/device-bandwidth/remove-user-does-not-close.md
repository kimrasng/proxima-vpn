# Existing Xray session revocation evidence

An isolated Xray 26.3.27 test used the existing `tests/device-bandwidth/` harness to remove an authenticated VLESS user from an inbound while Vision TCP and XUDP sessions were already active. The `RemoveUser` operation rejected a fresh connection but **did not terminate** either established transport:

- Existing Vision TCP forwarded another **245,760 bytes** during the next 1.5 seconds.
- Existing XUDP association echoed **3/3 UDP datagrams** after removal.

Therefore a scheduler eviction cannot claim immediate enforcement merely by calling `RemoveUser`; the node-local controlled egress must close all sessions belonging to the UUID, while central permits deny any attempted payload forwarding.

Test: `tests/device-bandwidth/removal_test.go`. This test changes no live node, user, or subscription. Isolated evidence was produced at `/tmp/xray-removal.HZEyIU/evidence.json` and is not a claim about production deployment.
