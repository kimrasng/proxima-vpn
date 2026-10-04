package bandwidth_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedLog struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *lockedLog) String() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

type xrayProcess struct {
	cmd  *exec.Cmd
	log  *lockedLog
	done chan error
	once sync.Once
}

func requireXray(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("BANDWIDTH_XRAY")
	if binary == "" {
		t.Skip("set BANDWIDTH_XRAY to an isolated real Xray binary")
	}
	return binary
}

func configFile(t *testing.T, label string, config any) string {
	t.Helper()
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), label+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testXrayConfig(t *testing.T, binary, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "run", "-test", "-config", path).CombinedOutput()
	if err != nil {
		t.Fatalf("Xray configuration rejected: %s: %v", sanitize(string(output)), err)
	}
}

func startXray(t *testing.T, binary, label string, config any, port int) *xrayProcess {
	t.Helper()
	path := configFile(t, label, config)
	testXrayConfig(t, binary, path)
	p := &xrayProcess{log: &lockedLog{}, done: make(chan error, 1)}
	p.cmd = exec.Command(binary, "run", "-config", path)
	p.cmd.Stdout = p.log
	p.cmd.Stderr = p.log
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() { p.stop(); recordEvidence(t, label+"_log", sanitize(p.log.String())) })
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-p.done:
			t.Fatalf("Xray exited: %v; %s", err, sanitize(p.log.String()))
		default:
		}
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			c.Close()
			return p
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Xray did not listen; %s", sanitize(p.log.String()))
	return nil
}

func (p *xrayProcess) stop() { p.once.Do(func() { _ = p.cmd.Process.Kill(); <-p.done }) }

func realityKeys(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private := base64.RawURLEncoding.EncodeToString(key.Bytes())
	public := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	rememberSecret(private)
	rememberSecret(public)
	return private, public
}

func xrayClientConfig(port, serverPort int, uuid, publicKey string) map[string]any {
	return map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"udp": true}}},
		"outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{
			"address": "127.0.0.1", "port": serverPort, "users": []any{map[string]any{"id": uuid, "flow": "xtls-rprx-vision", "encryption": "none"}},
		}}}, "streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{
			"serverName": "bandwidth.test", "fingerprint": "chrome", "password": publicKey, "shortId": "0123456789abcdef", "spiderX": "",
		}}}},
	}
}

var evidence = struct {
	sync.Mutex
	Data    map[string]any
	Secrets []string
}{Data: map[string]any{}}

func rememberSecret(secret string) {
	evidence.Lock()
	defer evidence.Unlock()
	evidence.Secrets = append(evidence.Secrets, secret)
}
func sanitize(text string) string {
	evidence.Lock()
	defer evidence.Unlock()
	for _, secret := range evidence.Secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
}
func recordEvidence(t *testing.T, label string, value any) {
	t.Helper()
	evidence.Lock()
	defer evidence.Unlock()
	evidence.Data[t.Name()+"/"+label] = value
	if path := os.Getenv("BANDWIDTH_EVIDENCE"); path != "" {
		data, err := json.MarshalIndent(evidence.Data, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Error(err)
		}
	}
}
