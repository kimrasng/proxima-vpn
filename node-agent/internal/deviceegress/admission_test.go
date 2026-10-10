package deviceegress_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestNewUUIDCapacityRejectedBeforeConnectSuccessAndKeepsIncumbent(t *testing.T) {
	s := startServer(t, allowAll, dialEcho(t))
	s.SetAdmitter(func(ctx context.Context, id string) error {
		if id == "device-b" {
			return errors.New("full")
		}
		return nil
	})
	old := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, old, 1); code != 0 {
		t.Fatalf("incumbent connect code %d", code)
	}
	assertActive(t, s, []string{"device-a"})
	newcomer := authenticate(t, s, "device-b", "secret-b")
	_ = newcomer.SetReadDeadline(time.Now().Add(time.Second))
	if code, _ := request(t, newcomer, 1); code == 0 {
		t.Fatal("newcomer received SOCKS success at capacity")
	}
	assertActive(t, s, []string{"device-a"})
	if _, err := old.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	var echoed [2]byte
	if _, err := io.ReadFull(old, echoed[:]); err != nil || string(echoed[:]) != "ok" {
		t.Fatalf("incumbent harmed: %s %v", echoed, err)
	}
}

func TestNewUUIDUDPAssociationDeniedBeforeSuccess(t *testing.T) {
	s := startServer(t, allowAll, func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("must not dial")
	})
	s.SetAdmitter(func(ctx context.Context, id string) error { return errors.New("full") })
	c := authenticate(t, s, "device-a", "secret-a")
	if code, _ := request(t, c, 3); code == 0 {
		t.Fatal("new UUID got UDP association at capacity")
	}
	assertActive(t, s, nil)
}
