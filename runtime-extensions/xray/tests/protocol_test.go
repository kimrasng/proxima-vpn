package historytests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func config(protocol string, ingress, target int, outbound bool) string {
	settings := map[string]any{}
	wireProtocol := protocol
	switch protocol {
	case "vless", "vmess":
		user := map[string]any{"id": identity, "email": "fixture@history"}
		if protocol == "vmess" {
			user["security"] = "auto"
		}
		if protocol == "vless" && outbound {
			user["encryption"] = "none"
		}
		if outbound {
			settings["vnext"] = []any{map[string]any{"address": "127.0.0.1", "port": ingress, "users": []any{user}}}
		} else {
			settings["clients"] = []any{user}
			if protocol == "vless" {
				settings["decryption"] = "none"
			}
		}
	case "trojan":
		if outbound {
			settings["servers"] = []any{map[string]any{"address": "127.0.0.1", "port": ingress, "password": "history-fixture"}}
		} else {
			settings["clients"] = []any{map[string]any{"password": "history-fixture", "email": "fixture@history"}}
		}
	case "shadowsocks", "shadowsocks2022", "shadowsocks2022multi":
		wireProtocol = "shadowsocks"
		method, password := "aes-128-gcm", "history-fixture"
		if strings.HasPrefix(protocol, "shadowsocks2022") {
			method, password = "2022-blake3-aes-128-gcm", "AAAAAAAAAAAAAAAAAAAAAA=="
		}
		if outbound {
			if protocol == "shadowsocks2022multi" {
				password += ":AQEBAQEBAQEBAQEBAQEBAQ=="
			}
			settings["servers"] = []any{map[string]any{"address": "127.0.0.1", "port": ingress, "method": method, "password": password}}
		} else {
			settings["method"], settings["password"], settings["network"] = method, password, "tcp,udp"
			if protocol == "shadowsocks2022multi" {
				settings["clients"] = []any{map[string]any{"password": "AQEBAQEBAQEBAQEBAQEBAQ==", "email": "fixture@history"}}
			}
		}
	}
	cfg := map[string]any{"log": map[string]any{"loglevel": "warning"}, "policy": map[string]any{"levels": map[string]any{"0": map[string]any{"uplinkOnly": 0, "downlinkOnly": 0}}}}
	if outbound {
		cfg["inbounds"] = []any{map[string]any{"listen": "127.0.0.1", "port": target, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": true}}}
		cfg["outbounds"] = []any{map[string]any{"protocol": wireProtocol, "settings": settings}}
	} else {
		cfg["inbounds"] = []any{map[string]any{"tag": "proof", "listen": "127.0.0.1", "port": ingress, "protocol": wireProtocol, "settings": settings}}
		cfg["outbounds"] = []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func socksConnect(t *testing.T, ingress, target int) *net.TCPConn {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ingress), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tcp := c.(*net.TCPConn)
	t.Cleanup(func() { tcp.Close() })
	if err := tcp.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := tcp.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 2)
	if _, err := io.ReadFull(tcp, b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, []byte{5, 0}) {
		t.Fatalf("SOCKS handshake %x", b)
	}
	if _, err := tcp.Write([]byte{5, 1, 0, 1, 127, 0, 0, 1, byte(target >> 8), byte(target)}); err != nil {
		t.Fatal(err)
	}
	b = make([]byte, 10)
	if _, err := io.ReadFull(tcp, b); err != nil {
		t.Fatal(err)
	}
	if b[1] != 0 {
		t.Fatalf("SOCKS request %x", b)
	}
	return tcp
}

func TestAuthenticatedProtocolExactPayload(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "shadowsocks", "shadowsocks2022", "shadowsocks2022multi"} {
		t.Run(protocol, func(t *testing.T) {
			// Given real patched server/client binaries and a subscribed collector.
			target, ingress, client := echoTCP(t), freePort(t), freePort(t)
			socket := socketPath(t)
			startRuntime(t, config(protocol, ingress, 0, false), socket)
			c := connectCollector(t, socket)
			c.apply(t, 1, true, epoch, protocol)
			startRuntime(t, config(protocol, ingress, client, true), "")
			conn := socksConnect(t, client, target)
			payload := bytes.Repeat([]byte{0x47}, 37)
			// When a known short flow transfers then actually closes.
			if _, err := conn.Write(payload); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, got) {
				t.Fatal("payload mismatch")
			}
			if err := conn.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, conn); err != nil {
				t.Fatal(err)
			}
			// Then the real runtime emits the final payload snapshot.
			end := c.end(t, len(payload), protocol)
			if end.Destination.ObservedIP == nil || *end.Destination.ObservedIP != "127.0.0.1" ||
				end.Destination.ObservedPort == nil || *end.Destination.ObservedPort != target {
				t.Fatal("direct outbound omitted its proven remote endpoint")
			}
		})
	}
}

func TestSOCKSOutboundCountsPayloadWithoutClaimingProxyAddress(t *testing.T) {
	// Given VLESS routed through an authenticated local SOCKS outbound.
	target, ingress, client, proxy := echoTCP(t), freePort(t), freePort(t), freePort(t)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(config("vless", ingress, 0, false)), &cfg); err != nil {
		t.Fatal(err)
	}
	direct := cfg["outbounds"]
	cfg["outbounds"] = []any{map[string]any{"protocol": "socks", "settings": map[string]any{
		"servers": []any{map[string]any{"address": "127.0.0.1", "port": proxy,
			"users": []any{map[string]any{"user": "fixture", "pass": "fixture-secret"}}}},
	}}}
	serverConfig, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	proxyConfig, err := json.Marshal(map[string]any{
		"log":    map[string]any{"loglevel": "warning"},
		"policy": cfg["policy"],
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": proxy, "protocol": "socks",
			"settings": map[string]any{"auth": "password",
				"accounts": []any{map[string]any{"user": "fixture", "pass": "fixture-secret"}}}}},
		"outbounds": direct,
	})
	if err != nil {
		t.Fatal(err)
	}
	startRuntime(t, string(proxyConfig), "")
	socket := socketPath(t)
	startRuntime(t, string(serverConfig), socket)
	c := connectCollector(t, socket)
	c.apply(t, 1, true, epoch, "vless")
	startRuntime(t, config("vless", ingress, client, true), "")
	conn := socksConnect(t, client, target)
	// When payload crosses the SOCKS forwarding boundary and both sides close.
	transfer(t, conn, 37)
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatal(err)
	}
	// Then headers are excluded and the proxy listener is not the destination.
	end := c.end(t, 37, "vless")
	if end.Destination.ObservedIP != nil || end.Destination.ObservedPort != nil {
		t.Fatal("SOCKS listener was misreported as the final endpoint")
	}
	if end.Destination.RequestedPort == nil || *end.Destination.RequestedPort != target {
		t.Fatal("original request destination lost")
	}
}
