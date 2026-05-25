package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// WireGuardConfig represents the full configuration for a WireGuard interface.
type WireGuardConfig struct {
	PrivateKey string          `json:"private_key"`
	ListenPort int             `json:"listen_port"`
	Address    string          `json:"address"` // e.g., "10.0.0.1/24"
	DNS        string          `json:"dns,omitempty"`
	Peers      []WireGuardPeer `json:"peers"`
}

// WireGuardPeer represents a single WireGuard peer entry.
type WireGuardPeer struct {
	PublicKey    string `json:"public_key"`
	AllowedIPs  string `json:"allowed_ips"` // e.g., "10.0.0.2/32"
	PresharedKey string `json:"preshared_key,omitempty"`
}

// WireGuardManager manages a WireGuard interface lifecycle.
type WireGuardManager struct {
	interfaceName string // e.g., "wg0"
	configPath    string // e.g., "/etc/wireguard/wg0.conf"
	listenPort    int
	privateKey    string
	mu            sync.Mutex
	running       bool
}

// NewWireGuardManager creates a new WireGuardManager instance.
func NewWireGuardManager(ifName string, listenPort int, configPath string) *WireGuardManager {
	if configPath == "" {
		configPath = filepath.Join("/etc/wireguard", ifName+".conf")
	}
	return &WireGuardManager{
		interfaceName: ifName,
		configPath:    configPath,
		listenPort:    listenPort,
	}
}

// Start brings up the WireGuard interface using wg-quick.
func (m *WireGuardManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("wireguard interface %s is already running", m.interfaceName)
	}

	cmd := exec.Command("wg-quick", "up", m.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wg-quick up %s: %w", m.interfaceName, err)
	}

	m.running = true
	return nil
}

// Stop tears down the WireGuard interface using wg-quick.
func (m *WireGuardManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	cmd := exec.Command("wg-quick", "down", m.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wg-quick down %s: %w", m.interfaceName, err)
	}

	m.running = false
	return nil
}

// IsRunning checks if the WireGuard interface exists by querying wg show.
func (m *WireGuardManager) IsRunning() bool {
	cmd := exec.Command("wg", "show", m.interfaceName)
	err := cmd.Run()
	return err == nil
}

// AddPeer adds a peer to the running WireGuard interface via wg set.
func (m *WireGuardManager) AddPeer(peer WireGuardPeer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	args := []string{"set", m.interfaceName, "peer", peer.PublicKey, "allowed-ips", peer.AllowedIPs}
	if peer.PresharedKey != "" {
		args = append(args, "preshared-key", "/dev/stdin")
	}

	cmd := exec.Command("wg", args...)
	if peer.PresharedKey != "" {
		cmd.Stdin = strings.NewReader(peer.PresharedKey)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg set add peer %s: %s: %w", peer.PublicKey, string(output), err)
	}

	return nil
}

// RemovePeer removes a peer from the running WireGuard interface via wg set.
func (m *WireGuardManager) RemovePeer(publicKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := exec.Command("wg", "set", m.interfaceName, "peer", publicKey, "remove")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg set remove peer %s: %s: %w", publicKey, string(output), err)
	}

	return nil
}

// GenerateConfig writes a WireGuard configuration file in standard INI format.
func (m *WireGuardManager) GenerateConfig(cfg WireGuardConfig) error {
	var sb strings.Builder

	sb.WriteString("[Interface]\n")
	sb.WriteString(fmt.Sprintf("PrivateKey = %s\n", cfg.PrivateKey))
	sb.WriteString(fmt.Sprintf("ListenPort = %d\n", cfg.ListenPort))
	if cfg.Address != "" {
		sb.WriteString(fmt.Sprintf("Address = %s\n", cfg.Address))
	}
	if cfg.DNS != "" {
		sb.WriteString(fmt.Sprintf("DNS = %s\n", cfg.DNS))
	}

	for _, peer := range cfg.Peers {
		sb.WriteString("\n[Peer]\n")
		sb.WriteString(fmt.Sprintf("PublicKey = %s\n", peer.PublicKey))
		sb.WriteString(fmt.Sprintf("AllowedIPs = %s\n", peer.AllowedIPs))
		if peer.PresharedKey != "" {
			sb.WriteString(fmt.Sprintf("PresharedKey = %s\n", peer.PresharedKey))
		}
	}

	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(m.configPath, []byte(sb.String()), 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	m.privateKey = cfg.PrivateKey
	m.listenPort = cfg.ListenPort
	return nil
}

// GenerateKeyPair generates a new WireGuard private/public key pair.
func (m *WireGuardManager) GenerateKeyPair() (privateKey, publicKey string, err error) {
	genCmd := exec.Command("wg", "genkey")
	privBytes, err := genCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("wg genkey: %w", err)
	}
	privateKey = strings.TrimSpace(string(privBytes))

	pubCmd := exec.Command("wg", "pubkey")
	pubCmd.Stdin = strings.NewReader(privateKey)
	pubBytes, err := pubCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("wg pubkey: %w", err)
	}
	publicKey = strings.TrimSpace(string(pubBytes))

	return privateKey, publicKey, nil
}
