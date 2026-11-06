package server

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/stretchr/testify/require"
)

func historyPutback(t *testing.T, cl client.Client, collector *historyCollector) {
	t.Helper()
	// The real app's sniff hook reads this request, rewrites the IP to its Host,
	// and reintroduces the same bytes. Neither sniff nor putback is counted twice.
	request := []byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, len(request))
		if _, err = io.ReadFull(conn, buf); err == nil {
			_, err = conn.Write([]byte("hook-ok"))
		}
		done <- err
	}()
	stream, err := cl.TCP(listener.Addr().String())
	require.NoError(t, err)
	defer stream.Close()
	require.NoError(t, stream.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = stream.Write(request)
	require.NoError(t, err)
	reply, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.Equal(t, "hook-ok", string(reply))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("putback target deadline")
	}
	port := listener.Addr().(*net.TCPAddr).Port
	end := collector.matching(t, "flow_end", 1, func(e nodeprov.HistoryEvent) bool {
		return e.Destination.RequestedPort != nil && *e.Destination.RequestedPort == port
	})[0]
	require.Equal(t, "127.0.0.1", *end.Destination.RequestedHost)
	require.Equal(t, "localhost", *end.Destination.EffectiveHost)
	require.Equal(t, nodeprov.HistoryU64(len(request)), *end.Bytes.Upload)
	require.Equal(t, nodeprov.HistoryU64(7), *end.Bytes.Download)
	require.True(t, end.Bytes.Final)
}
