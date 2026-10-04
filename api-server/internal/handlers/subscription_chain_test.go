package handlers

import (
	"strings"
	"testing"
)

func ptrInt(v int) *int       { return &v }
func ptrStr(v string) *string { return &v }

// exitNode is a direct chain to an exit serving several protocols: entry_port is
// NULL, so the client reaches the exit itself.
func exitNode() subscriptionNode {
	return subscriptionNode{
		ID:               "exit-1",
		Name:             "Japan 01",
		IP:               "203.0.113.10",
		Port:             443,
		Status:           "online",
		RealityPublicKey: "PUBKEY",
		RealityShortID:   "SHORTID",
		RealitySNI:       "reality.example.test",
		RealityPorts:     []int{443},
		RealityReady:     true,
		TLSCertFile:      ptrStr("/etc/cert.pem"),
		TLSKeyFile:       ptrStr("/etc/key.pem"),
		VmessPort:        ptrInt(8443),
		TrojanPort:       ptrInt(2083),
		SSPort:           ptrInt(8388),
		SSPassword:       ptrStr("sspass"),
		HY2Port:          ptrInt(36712),
		WGPort:           ptrInt(51820),
		ChainExitPort:    443,
	}
}

// relayedTo turns the same exit into a chain reached through a relay pool at
// entryHost:entryPort, forwarding to the exit's exitPort.
func relayedTo(entryHost string, entryPort, exitPort int) subscriptionNode {
	n := exitNode()
	n.EntryHost = entryHost
	n.EntryPort = ptrInt(entryPort)
	n.ChainExitPort = exitPort
	return n
}

// The whole point of a relay is that the exit's address never reaches the client.
// If this regresses, every subscription silently leaks the exit it was meant to
// hide.
func TestRelayedChainNeverAdvertisesTheExitAddress(t *testing.T) {
	node := relayedTo("5993.entry.example", 5993, 443)

	infos := buildNodeInfoList([]subscriptionNode{node}, "uuid-1", 0, nil, nil)
	if len(infos) != 1 {
		t.Fatalf("got %d entries, want exactly 1 for a single relayed chain", len(infos))
	}

	got := infos[0]
	if got.IP == node.IP {
		t.Errorf("subscription advertises the exit address %q", got.IP)
	}
	if got.IP != "5993.entry.example" {
		t.Errorf("IP = %q, want the chain entry host", got.IP)
	}
	if got.Port != 5993 {
		t.Errorf("Port = %d, want the entry port 5993", got.Port)
	}
}

// A relay never decrypts, so the client has to authenticate against the exit's own
// Reality keys. Substituting anything else would make the handshake fail.
func TestRelayedChainCarriesTheExitCredentials(t *testing.T) {
	infos := buildNodeInfoList(
		[]subscriptionNode{relayedTo("5993.entry.example", 5993, 443)},
		"uuid-1", 0, nil, nil,
	)
	if len(infos) != 1 {
		t.Fatalf("got %d entries, want 1", len(infos))
	}

	if infos[0].Protocol != "vless_reality" {
		t.Errorf("Protocol = %q, want vless_reality", infos[0].Protocol)
	}
	if infos[0].RealityPublicKey != "PUBKEY" || infos[0].RealityShortID != "SHORTID" {
		t.Errorf("Reality credentials = %q/%q, want the exit's PUBKEY/SHORTID",
			infos[0].RealityPublicKey, infos[0].RealityShortID)
	}
}

// exit_port is what makes a chain protocol-agnostic: the same chain shape carries
// whatever the exit runs on that port.
func TestRelayedChainProtocolFollowsTheExitPort(t *testing.T) {
	cases := map[int]string{
		443:   "vless_reality",
		8443:  "vmess_ws",
		2083:  "trojan_tls",
		8388:  "shadowsocks",
		36712: "hysteria2",
	}

	for exitPort, want := range cases {
		infos := buildNodeInfoList(
			[]subscriptionNode{relayedTo("h.example", 7000, exitPort)},
			"uuid-1", 0, nil, nil,
		)
		if len(infos) != 1 {
			t.Errorf("exit port %d: got %d entries, want 1", exitPort, len(infos))
			continue
		}
		if infos[0].Protocol != want {
			t.Errorf("exit port %d: Protocol = %q, want %q", exitPort, infos[0].Protocol, want)
		}
		if infos[0].Port != 7000 {
			t.Errorf("exit port %d: Port = %d, want the entry port 7000", exitPort, infos[0].Port)
		}
	}
}

// A direct chain still offers every protocol its exit runs, at the exit's own
// address - the behaviour that existed before chains and must not change.
func TestDirectChainStillOffersEveryProtocolAtTheExit(t *testing.T) {
	infos := buildNodeInfoList([]subscriptionNode{exitNode()}, "uuid-1", 0, nil, nil)
	if len(infos) < 5 {
		t.Fatalf("got %d entries, want one per protocol the exit serves", len(infos))
	}
	for _, info := range infos {
		if info.IP != "203.0.113.10" {
			t.Errorf("direct entry %q dials %q, want the exit address", info.Name, info.IP)
		}
	}
}

// A chain naming an exit port with no inbound behind it cannot be connected to, so
// it must be withheld rather than published as a dead endpoint.
func TestRelayedChainWithNoMatchingExitPortIsOmitted(t *testing.T) {
	infos := buildNodeInfoList(
		[]subscriptionNode{relayedTo("h.example", 7000, 9999)},
		"uuid-1", 0, nil, nil,
	)
	if len(infos) != 0 {
		t.Errorf("got %d entries, want none: exit port 9999 serves nothing", len(infos))
	}
}

// Legacy agents without an authenticated per-device egress acknowledgment must
// not be advertised as enforcing a limited plan.
func TestRelayedChainIsWithheldFromSpeedLimitedPlans(t *testing.T) {
	nodes := []subscriptionNode{relayedTo("h.example", 7000, 443)}

	if infos := buildNodeInfoList(nodes, "uuid-1", 50, nil, nil); len(infos) != 0 {
		t.Errorf("a speed-limited plan was offered %d relayed entries, want none", len(infos))
	}
}

// WireGuard needs the device's own keypair; without it the config cannot handshake.
func TestRelayedWireGuardChainNeedsDeviceKeys(t *testing.T) {
	node := relayedTo("wg.example", 7104, 51820)
	node.WGServerPrivateKey = ptrStr("aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789AbCdEfG=")

	if infos := buildNodeInfoList([]subscriptionNode{node}, "uuid-1", 0, nil, nil); len(infos) != 0 {
		t.Errorf("a wireguard chain was published without device keys (%d entries)", len(infos))
	}

	infos := buildNodeInfoList([]subscriptionNode{node}, "uuid-1", 0,
		ptrStr("aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789AbCdEfG="), ptrStr("10.66.0.2/32"))
	if len(infos) != 1 {
		t.Fatalf("got %d entries with device keys present, want 1", len(infos))
	}
	if infos[0].Protocol != "wireguard" {
		t.Errorf("Protocol = %q, want wireguard", infos[0].Protocol)
	}
	if infos[0].IP != "wg.example" || infos[0].Port != 7104 {
		t.Errorf("dialing %s:%d, want wg.example:7104", infos[0].IP, infos[0].Port)
	}
}

// entry_host is what lets the addresses behind a chain be replaced without
// reissuing client configs; with none set there is nothing to advertise but the
// relay's own address, which the query supplies in IP.
func TestRelayedChainWithBlankEntryHostIsOmitted(t *testing.T) {
	node := relayedTo("", 5993, 443)
	node.IP = "198.51.100.7"

	infos := buildNodeInfoList([]subscriptionNode{node}, "uuid-1", 0, nil, nil)
	if len(infos) != 0 {
		t.Fatalf("got %d malformed relayed entries, want none", len(infos))
	}
	if link, ok := relayedLink("uuid-1", node, "malformed"); ok || strings.Contains(link, node.IP) {
		t.Errorf("malformed relayed link = %q, ok=%t; exit address must not be used", link, ok)
	}
}

// The v2ray format carries one URI per endpoint, so a relayed chain must produce
// exactly one, pointing at the entry.
func TestRelayedLinkPointsAtTheEntry(t *testing.T) {
	link, ok := relayedLink("uuid-1", relayedTo("5993.entry.example", 5993, 443), "Japan 01")
	if !ok {
		t.Fatal("a valid relayed vless chain produced no link")
	}
	if !strings.Contains(link, "@5993.entry.example:5993") {
		t.Errorf("link does not dial the entry: %s", link)
	}
	if strings.Contains(link, "203.0.113.10") {
		t.Errorf("link leaks the exit address: %s", link)
	}
}

// WireGuard has no URI form, so a WireGuard chain must yield no link rather than a
// malformed one.
func TestRelayedWireGuardChainProducesNoLink(t *testing.T) {
	if _, ok := relayedLink("uuid-1", relayedTo("h.example", 7104, 51820), "Japan 01"); ok {
		t.Error("a wireguard chain produced a link; the v2ray format cannot express one")
	}
}

// The standalone .conf must not be built from a chain that forwards some other
// protocol: the tunnel would never come up.
func TestWireGuardConfSkipsChainsThatDoNotForwardWireGuard(t *testing.T) {
	key := ptrStr("aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789AbCdEfG=")

	vlessChain := relayedTo("h.example", 5993, 443)
	vlessChain.WGServerPrivateKey = key
	if _, ok := firstWireGuardNodeInfo([]subscriptionNode{vlessChain}, key, ptrStr("10.66.0.2/32")); ok {
		t.Error("a chain forwarding VLESS was used to build a WireGuard config")
	}

	wgChain := relayedTo("wg.example", 7104, 51820)
	wgChain.WGServerPrivateKey = key
	info, ok := firstWireGuardNodeInfo([]subscriptionNode{wgChain}, key, ptrStr("10.66.0.2/32"))
	if !ok {
		t.Fatal("a chain forwarding WireGuard produced no config")
	}
	if info.IP != "wg.example" || info.Port != 7104 {
		t.Errorf("conf dials %s:%d, want wg.example:7104", info.IP, info.Port)
	}
}
