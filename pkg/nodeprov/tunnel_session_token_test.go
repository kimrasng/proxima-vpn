package nodeprov

import (
	"testing"
)

func TestTunnelSessionTokenBindsExactAuthenticatedGeneration(t *testing.T) {
	exit := "00000000-0000-4000-8000-000000000001"
	connection := TunnelConnection{UUID: "00000000-0000-4000-8000-000000000002", PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}
	first, err := TunnelSessionToken(exit, connection)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := TunnelSessionToken(exit, connection)
	if err != nil || retry != first {
		t.Fatalf("same transport token changed: %q %v", retry, err)
	}
	cases := map[string]TunnelConnection{}
	changed := connection
	changed.PeerPort++
	cases["peer port"] = changed
	changed = connection
	changed.LocalPort++
	cases["local port"] = changed
	changed = connection
	changed.AcceptedAt++
	cases["acceptance time"] = changed
	changed = connection
	changed.UUID = "00000000-0000-4000-8000-000000000003"
	cases["credential"] = changed
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			token, err := TunnelSessionToken(exit, value)
			if err != nil {
				t.Fatal(err)
			}
			if token == first {
				t.Fatal("different transport generation shared token")
			}
		})
	}
	otherExit := "00000000-0000-4000-8000-000000000004"
	token, err := TunnelSessionToken(otherExit, connection)
	if err != nil || token == first {
		t.Fatal("different Exit shared token")
	}
}

func TestTunnelSessionTokenRejectsInvalidEvidence(t *testing.T) {
	connection := TunnelConnection{UUID: "00000000-0000-4000-8000-000000000002", PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}
	if _, err := TunnelSessionToken("not-a-uuid", connection); err == nil {
		t.Fatal("invalid Exit accepted")
	}
	connection.PeerPort = 0
	if _, err := TunnelSessionToken("00000000-0000-4000-8000-000000000001", connection); err == nil {
		t.Fatal("invalid transport accepted")
	}
}

func TestTunnelConnectionRejectsMappedUnspecifiedAndMulticastAddresses(t *testing.T) {
	base := TunnelConnection{UUID: "00000000-0000-4000-8000-000000000002", PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}
	for _, tc := range []struct {
		name string
		addr string
	}{
		{name: "unspecified", addr: "::ffff:0.0.0.0"},
		{name: "multicast", addr: "::ffff:224.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection := base
			connection.PeerIP = tc.addr
			if err := connection.Validate(); err == nil {
				t.Fatalf("mapped %s address accepted", tc.name)
			}
		})
	}
}

func TestTunnelSessionTokenCanonicalizesEquivalentAddresses(t *testing.T) {
	const exit = "00000000-0000-4000-8000-000000000001"
	base := TunnelConnection{UUID: "00000000-0000-4000-8000-000000000002", PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}
	for _, tc := range []struct {
		name                          string
		canonicalPeer, canonicalLocal string
		variantPeer, variantLocal     string
	}{
		{
			name:          "IPv4 and mapped IPv4",
			canonicalPeer: "203.0.113.2", canonicalLocal: "10.0.2.2",
			variantPeer: "::ffff:203.0.113.2", variantLocal: "::ffff:10.0.2.2",
		},
		{
			name:          "equivalent IPv6 spellings",
			canonicalPeer: "2001:db8::2", canonicalLocal: "2001:db8::1",
			variantPeer: "2001:0db8:0000:0000:0000:0000:0000:0002", variantLocal: "2001:0db8:0000:0000:0000:0000:0000:0001",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical := base
			canonical.PeerIP, canonical.LocalIP = tc.canonicalPeer, tc.canonicalLocal
			variant := base
			variant.PeerIP, variant.LocalIP = tc.variantPeer, tc.variantLocal
			want, err := TunnelSessionToken(exit, canonical)
			if err != nil {
				t.Fatal(err)
			}
			got, err := TunnelSessionToken(exit, variant)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("equivalent addresses produced different tokens: %q != %q", got, want)
			}
		})
	}
}
