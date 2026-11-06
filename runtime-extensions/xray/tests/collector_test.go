package historytests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type collector struct {
	conn  net.Conn
	lines chan []byte
	boot  nodeprov.HistoryUUID
}

func connectCollector(t *testing.T, path string) *collector {
	t.Helper()
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c := &collector{conn: conn, lines: make(chan []byte, 256)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(c.lines)
		s := bufio.NewScanner(conn)
		for s.Scan() {
			c.lines <- append(bytes.Clone(s.Bytes()), '\n')
		}
	}()
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Log(err)
		}
		<-done
	})
	var hello nodeprov.HistoryHello
	if err := json.Unmarshal(c.next(t), &hello); err != nil {
		t.Fatal(err)
	}
	if hello.RuntimeBuild != "xray-a4a3dff5-history-v1" {
		t.Fatalf("wrong build: %#v", hello)
	}
	c.boot = hello.ObserverBootID
	c.send(t, nodeprov.HistoryResume{Op: "resume", ObserverBootID: c.boot})
	return c
}

func (c *collector) send(t *testing.T, v any) {
	t.Helper()
	if err := json.NewEncoder(c.conn).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func (c *collector) next(t *testing.T) []byte {
	t.Helper()
	select {
	case line, ok := <-c.lines:
		if !ok {
			t.Fatal("history socket closed")
		}
		t.Logf("history %s", line)
		return line
	case <-time.After(15 * time.Second):
		t.Fatal("expected history event was not emitted")
	}
	return nil
}

func (c *collector) apply(t *testing.T, rev uint64, enabled bool, ep string, protocol string) []nodeprov.HistoryEvent {
	t.Helper()
	kind, labelKind, label := "device", "xray_email", "fixture@history"
	if protocol == "shadowsocks" || protocol == "shadowsocks2022" {
		kind, labelKind, label = "shared", "shared_inbound", "proof"
	}
	c.send(t, map[string]any{
		"op": "apply", "config_revision": strconv.FormatUint(rev, 10),
		"collection_epoch": ep, "enabled": enabled,
		"bindings": []any{map[string]any{
			"runtime": "xray", "inbound_tag": "proof", "label_kind": labelKind,
			"label": label, "credential_ref": credential, "kind": kind, "binding_revision": "1",
		}},
	})
	var events []nodeprov.HistoryEvent
	for {
		line := c.next(t)
		var control struct {
			Op string `json:"op"`
		}
		if err := json.Unmarshal(line, &control); err != nil {
			t.Fatal(err)
		}
		if control.Op == "applied" {
			return events
		}
		if control.Op != "" {
			t.Fatalf("unexpected control: %s", line)
		}
		e, err := nodeprov.DecodeHistoryEvent(line)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
}

func (c *collector) end(t *testing.T, n int, protocol string) nodeprov.HistoryEvent {
	return c.endBytes(t, uint64(n), uint64(n), protocol)
}

func (c *collector) endBytes(t *testing.T, upload, download uint64, protocol string) nodeprov.HistoryEvent {
	return c.endBytesWithStarts(t, upload, download, protocol, 1)
}

func (c *collector) endBytesWithStarts(t *testing.T, upload, download uint64, protocol string, expectedStarts int) nodeprov.HistoryEvent {
	t.Helper()
	eventProtocol := strings.TrimSuffix(protocol, "multi")
	starts := 0
	for {
		e, err := nodeprov.DecodeHistoryEvent(c.next(t))
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == "flow_start" {
			starts++
		}
		if e.Kind != "flow_end" {
			continue
		}
		if starts != expectedStarts {
			t.Fatalf("flow starts=%d", starts)
		}
		if e.Bytes.Upload == nil || e.Bytes.Download == nil || uint64(*e.Bytes.Upload) != upload || uint64(*e.Bytes.Download) != download || !e.Bytes.Final {
			t.Fatalf("wrong payload counters: %#v", e.Bytes)
		}
		if e.Protocol == nil || *e.Protocol != eventProtocol || e.TunnelID == nil {
			t.Fatalf("missing authenticated ownership: %#v", e)
		}
		if e.Auth.CredentialRef == nil || string(*e.Auth.CredentialRef) != credential {
			t.Fatalf("wrong binding: %#v", e.Auth)
		}
		if (protocol == "shadowsocks" || protocol == "shadowsocks2022") && (e.Auth.Kind != "shared" || e.Quality.Identity != "unknown") {
			t.Fatal("shared authentication became a guessed account")
		}
		if e.Lifecycle.EndSemantics == nil || *e.Lifecycle.EndSemantics != "local_owner_closed" {
			t.Fatal("wrong end scope")
		}
		final := e
		for {
			terminal, err := nodeprov.DecodeHistoryEvent(c.next(t))
			if err != nil {
				t.Fatal(err)
			}
			if terminal.Kind == "flow_end" {
				t.Fatal("duplicate terminal flow after quiescence")
			}
			if terminal.Kind != "tunnel_end" {
				continue
			}
			if terminal.TunnelID == nil || *terminal.TunnelID != *final.TunnelID ||
				terminal.Lifecycle.EndSemantics == nil || *terminal.Lifecycle.EndSemantics != "local_owner_closed" {
				t.Fatal("wrong actual transport closure")
			}
			return final
		}
	}
}
