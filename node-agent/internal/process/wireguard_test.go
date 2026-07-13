package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWireGuardGenerateConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wg0.conf")

	m := NewWireGuardManager("wg0", 51820, configPath)
	cfg := WireGuardConfig{
		PrivateKey: "server-priv",
		ListenPort: 51820,
		Address:    "10.0.0.1/24",
		DNS:        "1.1.1.1",
		Peers: []WireGuardPeer{
			{PublicKey: "peer-pub", AllowedIPs: "10.0.0.2/32"},
		},
	}

	if err := m.GenerateConfig(cfg); err != nil {
		t.Fatalf("GenerateConfig error: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	out := string(data)

	for _, want := range []string{
		"[Interface]",
		"PrivateKey = server-priv",
		"ListenPort = 51820",
		"Address = 10.0.0.1/24",
		"DNS = 1.1.1.1",
		"[Peer]",
		"PublicKey = peer-pub",
		"AllowedIPs = 10.0.0.2/32",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config missing %q, got:\n%s", want, out)
		}
	}
}

func TestWireGuardConfigFilePermissions(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wg0.conf")

	m := NewWireGuardManager("wg0", 51820, configPath)
	if err := m.GenerateConfig(WireGuardConfig{PrivateKey: "k", ListenPort: 51820}); err != nil {
		t.Fatalf("GenerateConfig error: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected config perm 0600, got %o", perm)
	}
}
