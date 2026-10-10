package deviceegress_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

type blockedWriteConn struct {
	net.Conn
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (c *blockedWriteConn) Write(payload []byte) (int, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockedWriteConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestBlockedWriteDoesNotAccumulateDeviceGrantsAcrossSessions(t *testing.T) {
	endpoint := echoTCP(t)
	grants := make(chan string, 10)
	entered := make(chan struct{}, 10)
	s := startServer(t, func(ctx context.Context, uuid string, direction devicebandwidth.Direction, size int) (devicebandwidth.PermitResponse, error) {
		grants <- uuid
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, endpoint)
		if err != nil {
			return nil, err
		}
		return &blockedWriteConn{Conn: c, entered: entered, closed: make(chan struct{})}, nil
	})
	connections := make([]net.Conn, 3)
	for i, uuid := range []string{"device-a", "device-a", "device-b"} {
		connections[i] = authenticate(t, s, uuid, "secret-"+uuid[len(uuid)-1:])
		if code, _ := request(t, connections[i], 1); code != 0 {
			t.Fatalf("CONNECT: %d", code)
		}
	}
	_, _ = connections[0].Write([]byte("blocked"))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("write not entered")
	}
	select {
	case uuid := <-grants:
		if uuid != "device-a" {
			t.Fatalf("first grant %s", uuid)
		}
	default:
		t.Fatal("write before grant")
	}
	_, _ = connections[1].Write([]byte("queued without grant"))
	_, _ = connections[2].Write([]byte("independent device"))
	select {
	case uuid := <-grants:
		if uuid != "device-b" {
			t.Fatalf("same-device grant accumulated behind a blocked write: %s", uuid)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked device prevented independent device from forwarding")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("independent write not entered")
	}
	select {
	case uuid := <-grants:
		t.Fatalf("unexpected additional grant for %s", uuid)
	case <-time.After(50 * time.Millisecond):
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
