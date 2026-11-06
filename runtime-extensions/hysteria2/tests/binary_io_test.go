package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/stretchr/testify/require"
)

type historyCollector struct {
	conn     net.Conn
	mu       sync.Mutex
	events   []nodeprov.HistoryEvent
	controls []nodeprov.HistoryControl
	changed  chan struct{}
	err      error
	done     chan struct{}
	file     *os.File
}

func collectHistory(t *testing.T, socket, evidence string) *historyCollector {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	require.NoError(t, err)
	file, err := os.Create(evidence)
	require.NoError(t, err)
	c := &historyCollector{conn: conn, changed: make(chan struct{}), done: make(chan struct{}), file: file}
	go func() {
		defer close(c.done)
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadBytes('\n')
			c.mu.Lock()
			if err == nil {
				_, err = file.Write(line)
			}
			if err == nil {
				var tag struct {
					Op string `json:"op"`
				}
				err = json.Unmarshal(line, &tag)
				if err == nil && tag.Op == "" {
					var event nodeprov.HistoryEvent
					event, err = nodeprov.DecodeHistoryEvent(line)
					c.events = append(c.events, event)
				} else if err == nil {
					var frame nodeprov.HistoryControl
					frame, err = nodeprov.DecodeHistoryControl(line)
					if e, ok := frame.(nodeprov.HistoryError); ok {
						err = &e
					}
					c.controls = append(c.controls, frame)
				}
			}
			c.err = err
			close(c.changed)
			c.changed = make(chan struct{})
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		conn.Close()
		<-c.done
		require.NoError(t, file.Close())
	})
	return c
}

func (c *historyCollector) wait(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		ok, err, changed := ready(), c.err, c.changed
		c.mu.Unlock()
		if ok {
			return
		}
		require.NoError(t, err, description)
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("deadline: %s", description)
		}
	}
}

func (c *historyCollector) send(t *testing.T, frame nodeprov.HistoryControl) {
	t.Helper()
	require.NoError(t, c.conn.SetWriteDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, json.NewEncoder(c.conn).Encode(frame))
}

func (c *historyCollector) hello(t *testing.T) nodeprov.HistoryHello {
	t.Helper()
	var hello nodeprov.HistoryHello
	c.wait(t, "hello", func() bool {
		for _, frame := range c.controls {
			if h, ok := frame.(nodeprov.HistoryHello); ok {
				hello = h
				return true
			}
		}
		return false
	})
	return hello
}

func (c *historyCollector) apply(t *testing.T, revision nodeprov.HistoryU64, epoch nodeprov.HistoryUUID, enabled bool) nodeprov.HistoryApplied {
	t.Helper()
	bindings := []nodeprov.HistoryBinding{
		{Runtime: "hysteria2", InboundTag: "hysteria2", LabelKind: "hysteria_auth_id", Label: "alice", CredentialRef: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Kind: "device", BindingRevision: 1},
		{Runtime: "hysteria2", InboundTag: "hysteria2", LabelKind: "hysteria_auth_id", Label: "bob", CredentialRef: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Kind: "device", BindingRevision: 1},
	}
	c.send(t, nodeprov.HistoryApply{Op: "apply", ConfigRevision: revision, CollectionEpoch: &epoch, Enabled: enabled, Bindings: bindings})
	var applied nodeprov.HistoryApplied
	c.wait(t, fmt.Sprintf("applied revision %d", revision), func() bool {
		for _, frame := range c.controls {
			if a, ok := frame.(nodeprov.HistoryApplied); ok && a.ConfigRevision == revision {
				applied = a
				return true
			}
		}
		return false
	})
	return applied
}

func (c *historyCollector) matching(t *testing.T, kind string, n int, predicate func(nodeprov.HistoryEvent) bool) []nodeprov.HistoryEvent {
	t.Helper()
	var result []nodeprov.HistoryEvent
	c.wait(t, fmt.Sprintf("%d %s events", n, kind), func() bool {
		result = nil
		for _, e := range c.events {
			if e.Kind == kind && (predicate == nil || predicate(e)) {
				result = append(result, e)
			}
		}
		return len(result) >= n
	})
	return result
}

func historyTCPEcho(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(30 * time.Second))
				buf := make([]byte, 19)
				for {
					if _, err := io.ReadFull(conn, buf); err != nil {
						return
					}
					if _, err := conn.Write([]byte("reply!!")); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done; wg.Wait() })
	return listener.Addr().String()
}

func historyUDPEcho(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65535)
		for {
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := conn.WriteTo(buf[:n], peer); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { conn.Close(); <-done })
	return conn.LocalAddr().String()
}

func historyExchange(t *testing.T, conn net.Conn) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err := conn.Write([]byte("abcdefghijklmnopqrs"))
	require.NoError(t, err)
	b := make([]byte, 7)
	_, err = io.ReadFull(conn, b)
	require.NoError(t, err)
	require.True(t, bytes.Equal(b, []byte("reply!!")))
}
