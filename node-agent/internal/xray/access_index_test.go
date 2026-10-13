package xray

import (
	"bytes"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const accessUUID = "00000000-0000-4000-8000-000000000001"

func accessLine(peer, credential string) string {
	return fmt.Sprintf("2026/10/13 02:00:00.123456 from %s accepted tcp:example.test:443 [vless-in -> device-egress] email: %s@proxima", peer, credential)
}

func TestAccessIndexParsesAuthenticatedTransportPeersOnly(t *testing.T) {
	now := time.Now()
	for _, address := range []string{"203.0.113.2:40000", "tcp:203.0.113.2:40000", "tcp:[2001:db8::2]:40000"} {
		got, ok := parseAccessPeer(accessLine(address, accessUUID), now)
		if !ok || got.UUID != accessUUID || got.AcceptedAt != now {
			t.Fatalf("%s: %+v %v", address, got, ok)
		}
	}
	for _, line := range []string{
		accessLine("udp:203.0.113.2:40000", accessUUID),
		accessLine("203.0.113.2:0", accessUUID),
		accessLine("203.0.113.2:40000", "invalid-credential"),
		strings.Replace(accessLine("203.0.113.2:40000", accessUUID), "accepted", "rejected", 1),
		strings.Replace(accessLine("203.0.113.2:40000", accessUUID), "@proxima", "@other", 1),
		"from 127.0.0.1:30000 accepted tcp:127.0.0.1:10085 [api -> api]",
	} {
		if _, ok := parseAccessPeer(line, now); ok {
			t.Errorf("accepted unrelated/invalid line %q", line)
		}
	}
}

func TestAccessIndexPreservesFirstObservationAndRejectsConflictingOwnership(t *testing.T) {
	i := NewAccessIndex()
	peer := netip.MustParseAddrPort("203.0.113.2:40000")
	now := time.Now()
	i.Observe(accessLine(peer.String(), accessUUID), now)
	i.Observe(accessLine(peer.String(), accessUUID), now.Add(time.Minute))
	got, known, complete := i.Lookup(peer)
	if !known || !complete || got.AcceptedAt != now {
		t.Fatalf("repeated destination changed start: %+v %v %v", got, known, complete)
	}
	i.Observe(accessLine(peer.String(), "00000000-0000-4000-8000-000000000002"), now)
	if _, known, _ := i.Lookup(peer); known {
		t.Fatal("conflicting tuple ownership guessed a credential")
	}
	i.Reset()
	i.Observe(accessLine(peer.String(), accessUUID), now.Add(time.Hour))
	if _, known, complete := i.Lookup(peer); !known || !complete {
		t.Fatal("new process generation retained ambiguity")
	}
}

func TestAccessIndexPrunesOnlyConfirmedAbsentOldPeers(t *testing.T) {
	i := NewAccessIndex()
	now := time.Now()
	old := netip.MustParseAddrPort("203.0.113.2:40000")
	recent := netip.MustParseAddrPort("203.0.113.3:40001")
	live := netip.MustParseAddrPort("203.0.113.4:40002")
	i.Observe(accessLine(old.String(), accessUUID), now.Add(-time.Minute))
	i.Observe(accessLine(recent.String(), accessUUID), now)
	i.Observe(accessLine(live.String(), accessUUID), now.Add(-time.Hour))
	i.PruneAbsent(map[netip.AddrPort]bool{live: true}, now)
	if _, known, _ := i.Lookup(old); known {
		t.Fatal("old absent peer retained")
	}
	if _, known, _ := i.Lookup(recent); !known {
		t.Fatal("fresh log/socket-dump race discarded")
	}
	if _, known, _ := i.Lookup(live); !known {
		t.Fatal("live peer discarded")
	}
}

func TestAccessWriterIndexesFragmentedLinesWithoutChangingOutput(t *testing.T) {
	i := NewAccessIndex()
	var out bytes.Buffer
	w := &accessWriter{destination: &out, index: i}
	line := accessLine("203.0.113.2:40000", accessUUID) + "\n"
	for _, part := range []string{line[:20], line[20:70], line[70:]} {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if out.String() != line {
		t.Fatal("process output was changed")
	}
	if _, known, _ := i.Lookup(netip.MustParseAddrPort("203.0.113.2:40000")); !known {
		t.Fatal("fragmented access record was lost")
	}
}

func TestAccessWriterSkipsOversizedLinesAndRecovers(t *testing.T) {
	i := NewAccessIndex()
	var out bytes.Buffer
	w := &accessWriter{destination: &out, index: i}
	bad := strings.Repeat("x", maxAccessLine+1) + accessLine("203.0.113.2:40000", accessUUID) + "\n"
	if _, err := w.Write([]byte(bad)); err != nil {
		t.Fatal(err)
	}
	if _, known, _ := i.Lookup(netip.MustParseAddrPort("203.0.113.2:40000")); known {
		t.Fatal("oversized record was indexed")
	}
	if _, err := w.Write([]byte(accessLine("203.0.113.3:40001", accessUUID) + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, known, _ := i.Lookup(netip.MustParseAddrPort("203.0.113.3:40001")); !known {
		t.Fatal("parser did not recover after oversized record")
	}
}
