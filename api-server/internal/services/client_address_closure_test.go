package services

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

const (
	closureExitA = "00000000-0000-4000-8000-0000000000a1"
	closureExitB = "00000000-0000-4000-8000-0000000000b2"
	closureCred  = "00000000-0000-4000-8000-000000000001"
)

func closureConn(peerIP string, peerPort int, acceptedAt int64) nodeprov.TunnelConnection {
	return nodeprov.TunnelConnection{UUID: closureCred, PeerIP: peerIP, PeerPort: peerPort, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: acceptedAt, IdleMillis: 5}
}

func closureObservation(t *testing.T, transports ...ObservedAddressTransport) ClientAddressObservation {
	t.Helper()
	tagged := make([]exitConnection, 0, len(transports))
	for _, tr := range transports {
		tagged = append(tagged, exitConnection{exitID: tr.ExitID, connection: tr.Connection})
	}
	got, err := joinClientAddresses(tagged, map[string]bool{}, nil)
	if err != nil || !got.Complete {
		t.Fatalf("observation: %+v %v", got, err)
	}
	return got
}

func TestClientAddressClosureTargetsRejectsIncompleteObservation(t *testing.T) {
	tr := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40000, 10)}
	obs := closureObservation(t, tr)
	obs.Complete = false
	if got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.2"}); err == nil || got != nil {
		t.Fatalf("incomplete observation accepted: %+v %v", got, err)
	}
}

func TestClientAddressClosureTargetsRejectsUnknownDeniedAddress(t *testing.T) {
	tr := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40000, 10)}
	obs := closureObservation(t, tr)
	got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.2", "198.51.100.9"})
	if err == nil || got != nil {
		t.Fatalf("unknown denied address accepted: %+v %v", got, err)
	}
	if strings.Contains(err.Error(), "198.51.100.9") || strings.Contains(err.Error(), "203.0.113.2") {
		t.Fatalf("error leaks raw address: %v", err)
	}
}

func TestClientAddressClosureTargetsRejectsAddressWithoutTransports(t *testing.T) {
	tr := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40000, 10)}
	obs := closureObservation(t, tr)
	// Present in Addresses but nothing resolved to it: never guess.
	obs.Addresses = append(obs.Addresses, "203.0.113.3")
	if got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.3"}); err == nil || got != nil {
		t.Fatalf("address without transports accepted: %+v %v", got, err)
	}
	obs.Transports["203.0.113.3"] = []ObservedAddressTransport{}
	if got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.3"}); err == nil || got != nil {
		t.Fatalf("address with empty transports accepted: %+v %v", got, err)
	}
	// The wrapper does not know Exit ownership, so it can never drive closure.
	wrapped, err := JoinClientAddresses([]nodeprov.TunnelConnection{closureConn("203.0.113.2", 40000, 10)}, map[string]bool{}, nil)
	if err != nil || !wrapped.Complete {
		t.Fatalf("wrapper: %+v %v", wrapped, err)
	}
	if got, err := ClientAddressClosureTargets(wrapped, []string{"203.0.113.2"}); err == nil || got != nil {
		t.Fatalf("exported join output drove closure: %+v %v", got, err)
	}
}

func TestClientAddressClosureTargetsRejectsInvalidExitID(t *testing.T) {
	for _, exitID := range []string{"", "not-a-uuid", strings.ToUpper(closureExitA), "{" + closureExitA + "}"} {
		tr := ObservedAddressTransport{ExitID: exitID, Connection: closureConn("203.0.113.2", 40000, 10)}
		obs := ClientAddressObservation{
			Complete:   true,
			Addresses:  []string{"203.0.113.2"},
			Transports: map[string][]ObservedAddressTransport{"203.0.113.2": {tr}},
		}
		if got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.2"}); err == nil || got != nil {
			t.Fatalf("exit id %q accepted: %+v %v", exitID, got, err)
		}
	}
	// An invalid connection is rejected too, never silently skipped.
	bad := closureConn("203.0.113.2", 0, 10)
	obs := ClientAddressObservation{
		Complete:   true,
		Addresses:  []string{"203.0.113.2"},
		Transports: map[string][]ObservedAddressTransport{"203.0.113.2": {{ExitID: closureExitA, Connection: bad}}},
	}
	if got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.2"}); err == nil || got != nil {
		t.Fatalf("invalid connection accepted: %+v %v", got, err)
	}
}

func TestClientAddressClosureTargetsDeduplicates(t *testing.T) {
	tr := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40000, 10)}
	sameWithDifferentIdle := tr
	sameWithDifferentIdle.Connection.IdleMillis = 9999
	obs := closureObservation(t, tr, tr, sameWithDifferentIdle)
	got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.2", "203.0.113.2"})
	if err != nil || len(got) != 1 || got[0].ExitID != closureExitA || got[0].Connection.PeerPort != 40000 {
		t.Fatalf("dedupe: %+v %v", got, err)
	}
}

func TestClientAddressClosureTargetsDeterministicOrder(t *testing.T) {
	all := []ObservedAddressTransport{
		{ExitID: closureExitB, Connection: closureConn("203.0.113.2", 40001, 5)},
		{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40002, 7)},
		{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40001, 9)},
		{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40001, 3)},
	}
	want := []ObservedAddressTransport{all[3], all[2], all[1], all[0]}
	for _, order := range [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}} {
		shuffled := make([]ObservedAddressTransport, 0, len(order))
		for _, i := range order {
			shuffled = append(shuffled, all[i])
		}
		got, err := ClientAddressClosureTargets(closureObservation(t, shuffled...), []string{"203.0.113.2"})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("order %v: got=%+v err=%v", order, got, err)
		}
	}
}

func TestClientAddressClosureTargetsCoversMultipleExitsForOneAddress(t *testing.T) {
	keep := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("198.51.100.7", 41000, 1)}
	a := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40000, 10)}
	b1 := ObservedAddressTransport{ExitID: closureExitB, Connection: closureConn("203.0.113.2", 40001, 11)}
	b2 := ObservedAddressTransport{ExitID: closureExitB, Connection: closureConn("203.0.113.2", 40002, 12)}
	obs := closureObservation(t, keep, b2, a, b1)
	got, err := ClientAddressClosureTargets(obs, []string{"203.0.113.2"})
	if err != nil || !reflect.DeepEqual(got, []ObservedAddressTransport{a, b1, b2}) {
		t.Fatalf("targets: %+v %v", got, err)
	}
	for _, target := range got {
		if target.Connection.PeerIP == "198.51.100.7" {
			t.Fatalf("allowed address transport targeted: %+v", target)
		}
	}
}

func TestClientAddressObservationRecordsEntryRelayedTransport(t *testing.T) {
	relayed := closureConn("10.0.2.1", 40000, 10)
	flow := nodeprov.RelayFlow{PeerIP: relayed.PeerIP, PeerPort: relayed.PeerPort, ExitIP: relayed.LocalIP, ExitPort: relayed.LocalPort, ClientIP: "203.0.113.2"}
	direct := closureConn("198.51.100.7", 41000, 20)
	got, err := joinClientAddresses(
		[]exitConnection{{exitID: closureExitA, connection: relayed}, {exitID: closureExitB, connection: direct}},
		map[string]bool{"10.0.2.1": true},
		[]nodeprov.RelayFlowReport{{Known: true, Flows: []nodeprov.RelayFlow{flow}}},
	)
	if err != nil || !got.Complete {
		t.Fatalf("join: %+v %v", got, err)
	}
	if !reflect.DeepEqual(got.Addresses, []string{"198.51.100.7", "203.0.113.2"}) {
		t.Fatalf("addresses: %v", got.Addresses)
	}
	if _, isEntry := got.Transports["10.0.2.1"]; isEntry {
		t.Fatalf("Entry relay IP recorded as a client address: %+v", got.Transports)
	}
	if !reflect.DeepEqual(got.Transports["203.0.113.2"], []ObservedAddressTransport{{ExitID: closureExitA, Connection: relayed}}) {
		t.Fatalf("relayed transport: %+v", got.Transports)
	}
	if !reflect.DeepEqual(got.Transports["198.51.100.7"], []ObservedAddressTransport{{ExitID: closureExitB, Connection: direct}}) {
		t.Fatalf("direct transport: %+v", got.Transports)
	}
	targets, err := ClientAddressClosureTargets(got, []string{"203.0.113.2"})
	if err != nil || !reflect.DeepEqual(targets, []ObservedAddressTransport{{ExitID: closureExitA, Connection: relayed}}) {
		t.Fatalf("targets: %+v %v", targets, err)
	}
	// The exported wrapper keeps its address/start behavior and does not claim ownership.
	wrapped, err := JoinClientAddresses([]nodeprov.TunnelConnection{relayed, direct}, map[string]bool{"10.0.2.1": true}, []nodeprov.RelayFlowReport{{Known: true, Flows: []nodeprov.RelayFlow{flow}}})
	if err != nil || !wrapped.Complete || !reflect.DeepEqual(wrapped.Addresses, got.Addresses) || !reflect.DeepEqual(wrapped.Starts, got.Starts) || len(wrapped.Transports) != 0 {
		t.Fatalf("wrapper: %+v %v", wrapped, err)
	}
}

func TestClientAddressObservationAmbiguousFlowRecordsNothing(t *testing.T) {
	relayed := closureConn("10.0.2.1", 40000, 10)
	flow := nodeprov.RelayFlow{PeerIP: relayed.PeerIP, PeerPort: relayed.PeerPort, ExitIP: relayed.LocalIP, ExitPort: relayed.LocalPort, ClientIP: "203.0.113.2"}
	other := flow
	other.ClientIP = "203.0.113.3"
	reports := []nodeprov.RelayFlowReport{{Known: true, Flows: []nodeprov.RelayFlow{flow, other}}}
	got, err := joinClientAddresses([]exitConnection{{exitID: closureExitA, connection: relayed}}, map[string]bool{"10.0.2.1": true}, reports)
	if err != nil || got.Complete {
		t.Fatalf("ambiguous flow accepted: %+v %v", got, err)
	}
	if len(got.Addresses) != 0 || len(got.Transports) != 0 || len(got.Starts) != 0 {
		t.Fatalf("ambiguous transport recorded: %+v", got)
	}
	// Missing attribution is equally ambiguous.
	got, err = joinClientAddresses([]exitConnection{{exitID: closureExitA, connection: relayed}}, map[string]bool{"10.0.2.1": true}, nil)
	if err != nil || got.Complete || len(got.Transports) != 0 {
		t.Fatalf("missing flow accepted: %+v %v", got, err)
	}
	if targets, err := ClientAddressClosureTargets(got, []string{"203.0.113.2"}); err == nil || targets != nil {
		t.Fatalf("incomplete observation produced targets: %+v %v", targets, err)
	}
}

func closureTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR required")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestQueueClientAddressClosuresRequiresRedis(t *testing.T) {
	tr := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40000, 10)}
	if n, err := QueueClientAddressClosures(context.Background(), nil, closureObservation(t, tr), []string{"203.0.113.2"}); err == nil || n != 0 {
		t.Fatalf("nil redis accepted: %d %v", n, err)
	}
}

func TestQueueClientAddressClosuresQueuesEveryTransportOfDeniedAddress(t *testing.T) {
	rdb := closureTestRedis(t)
	ctx := context.Background()
	exitA, exitB := uuid.NewString(), uuid.NewString()
	for _, node := range []string{exitA, exitB} {
		key := "node:" + node + ":transport_closures"
		t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })
	}
	credential := uuid.NewString()
	conn := func(peer string, port int, acceptedAt int64) nodeprov.TunnelConnection {
		c := closureConn(peer, port, acceptedAt)
		c.UUID = credential
		return c
	}
	denied1 := ObservedAddressTransport{ExitID: exitA, Connection: conn("203.0.113.2", 40000, 10)}
	denied2 := ObservedAddressTransport{ExitID: exitB, Connection: conn("203.0.113.2", 40001, 11)}
	allowed := ObservedAddressTransport{ExitID: exitA, Connection: conn("198.51.100.7", 41000, 12)}
	obs := closureObservation(t, denied1, denied2, allowed)

	// Fail closed before queueing anything.
	if n, err := QueueClientAddressClosures(ctx, rdb, obs, []string{"203.0.113.2", "192.0.2.1"}); err == nil || n != 0 {
		t.Fatalf("unknown denied address queued: %d %v", n, err)
	}
	if pending, err := PendingTransportClosures(ctx, rdb, exitA); err != nil || len(pending) != 0 {
		t.Fatalf("queued despite error: %+v %v", pending, err)
	}

	n, err := QueueClientAddressClosures(ctx, rdb, obs, []string{"203.0.113.2"})
	if err != nil || n != 2 {
		t.Fatalf("queued=%d err=%v", n, err)
	}
	pendingA, err := PendingTransportClosures(ctx, rdb, exitA)
	if err != nil || len(pendingA) != 1 || pendingA[0].Connection != denied1.Connection {
		t.Fatalf("exit A: %+v %v", pendingA, err)
	}
	pendingB, err := PendingTransportClosures(ctx, rdb, exitB)
	if err != nil || len(pendingB) != 1 || pendingB[0].Connection != denied2.Connection {
		t.Fatalf("exit B: %+v %v", pendingB, err)
	}
}

func TestClientAddressStartsPreserveTransports(t *testing.T) {
	rdb := closureTestRedis(t)
	ctx := context.Background()
	user := uuid.NewString()
	t.Cleanup(func() {
		_ = rdb.Del(ctx, "account:"+user+":client_addresses", "account:"+user+":client_address_starts").Err()
	})
	tr := ObservedAddressTransport{ExitID: closureExitA, Connection: closureConn("203.0.113.2", 40000, 10)}
	obs := closureObservation(t, tr)
	got, err := PreserveClientAddressStarts(ctx, rdb, user, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Transports, obs.Transports) || len(got.Transports["203.0.113.2"]) != 1 {
		t.Fatalf("transports lost: %+v", got.Transports)
	}
}
