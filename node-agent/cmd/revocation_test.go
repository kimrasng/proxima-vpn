package main

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/deviceegress"
	"github.com/proximavpn/proxima-vpn/pkg/devicebandwidth"
)

func TestRevocationWatchdogClosesIdleSessionDuringStalledFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := deviceegress.New(func(context.Context, string, devicebandwidth.Direction, int) (devicebandwidth.PermitResponse, error) {
		return devicebandwidth.PermitResponse{Allowed: true}, nil
	})
	if err := server.SetCredentials([]devicebandwidth.Credential{{UUID: "device-a", Password: "secret"}}); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	firstFetched := make(chan struct{})
	secondFetched := make(chan struct{})
	go revokedUUIDPollLoop(ctx, func(ctx context.Context) (client.RevocationSnapshot, error) {
		select {
		case <-firstFetched:
			close(secondFetched)
			<-ctx.Done()
			return client.RevocationSnapshot{}, ctx.Err()
		default:
			close(firstFetched)
			return client.RevocationSnapshot{RevokedUUIDs: []string{}}, nil
		}
	}, func(context.Context, client.Revocation) error { return nil }, server)
	<-firstFetched
	// Wait for the completed first snapshot before opening an idle session.
	var conn net.Conn
	var err error
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		conn, err = net.Dial("tcp", server.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
		_, _ = conn.Write([]byte{5, 1, 2})
		var response [2]byte
		if _, err = io.ReadFull(conn, response[:]); err == nil && response == [2]byte{5, 2} {
			_, _ = conn.Write([]byte{1, 8, 'd', 'e', 'v', 'i', 'c', 'e', '-', 'a', 6, 's', 'e', 'c', 'r', 'e', 't'})
			if _, err = io.ReadFull(conn, response[:]); err == nil && response == [2]byte{1, 0} {
				break
			}
		}
		_ = conn.Close()
		conn = nil
		time.Sleep(10 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("authentication never succeeded: %v", err)
	}
	defer func() { _ = conn.Close() }()
	<-secondFetched
	_ = conn.SetReadDeadline(time.Now().Add(6 * time.Second))
	var b [1]byte
	if _, err := conn.Read(b[:]); err == nil {
		t.Fatal("idle session unexpectedly sent data")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("watchdog left idle session open during stalled fetch")
	}
}
