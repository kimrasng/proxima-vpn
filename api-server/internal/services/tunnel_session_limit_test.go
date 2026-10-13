package services

import (
	"testing"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestSessionCapCountsSameIPAndUUIDAsSeparateTransports(t *testing.T) {
	c := nodeprov.TunnelConnection{UUID: "00000000-0000-4000-8000-000000000001", PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}
	second := c
	second.PeerPort = 40001
	second.AcceptedAt = 200
	obs := TunnelSessionObservation{Complete: true, Sessions: []ObservedTunnelSession{{ExitID: "00000000-0000-4000-8000-000000000010", Connection: c}, {ExitID: "00000000-0000-4000-8000-000000000010", Connection: second}, {ExitID: "00000000-0000-4000-8000-000000000010", Connection: c}}}
	key, err := TunnelSessionKey(obs.Sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	shared, err := nodeprov.TunnelSessionToken(obs.Sessions[0].ExitID, obs.Sessions[0].Connection)
	if err != nil || key != shared {
		t.Fatalf("control-plane and shared token differ: %q %q %v", key, shared, err)
	}
	count, err := CountTunnelSessions(obs)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	targets, err := NewestTunnelSessions(obs, 1)
	if err != nil || len(targets) != 1 || targets[0].Connection.PeerPort != 40001 {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	obs.Sessions = append(obs.Sessions, ObservedTunnelSession{ExitID: "00000000-0000-4000-8000-000000000011", Connection: c})
	count, err = CountTunnelSessions(obs)
	if err != nil || count != 3 {
		t.Fatalf("cross Exit count=%d err=%v", count, err)
	}
}

func TestSessionCapRejectsPartialAndAmbiguousEvidence(t *testing.T) {
	if _, err := CountTunnelSessions(TunnelSessionObservation{}); err == nil {
		t.Fatal("partial treated as zero")
	}
	c := nodeprov.TunnelConnection{UUID: "00000000-0000-4000-8000-000000000001", PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: 100}
	conflicting := c
	conflicting.UUID = "00000000-0000-4000-8000-000000000002"
	obs := TunnelSessionObservation{Complete: true, Sessions: []ObservedTunnelSession{{ExitID: "00000000-0000-4000-8000-000000000010", Connection: c}, {ExitID: "00000000-0000-4000-8000-000000000010", Connection: conflicting}}}
	if _, err := CountTunnelSessions(obs); err == nil {
		t.Fatal("conflicting ownership counted")
	}
	conflicting = c
	conflicting.PeerPort++
	obs.Sessions[1].Connection = conflicting
	if _, err := NewestTunnelSessions(obs, 1); err == nil {
		t.Fatal("tied sessions guessed target")
	}
}
