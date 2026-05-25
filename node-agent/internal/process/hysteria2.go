package process

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultHysteria2BinaryPath = "/usr/local/bin/hysteria"
	DefaultHysteria2ConfigPath = "/etc/node-agent/hysteria2-config.json"

	hysteria2StopTimeout = 10 * time.Second
)

// Hysteria2User represents a user entry in the Hysteria2 config.
type Hysteria2User struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// Hysteria2Config represents the Hysteria2 server configuration.
type Hysteria2Config struct {
	Listen    string              `json:"listen"`
	TLS       Hysteria2TLS        `json:"tls"`
	Auth      *Hysteria2Auth      `json:"auth,omitempty"`
	Users     []Hysteria2User     `json:"users,omitempty"`
	Bandwidth *Hysteria2Bandwidth `json:"bandwidth,omitempty"`
}

// Hysteria2TLS holds TLS certificate configuration.
type Hysteria2TLS struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

// Hysteria2Auth holds authentication configuration.
type Hysteria2Auth struct {
	Type     string `json:"type"`
	Password string `json:"password,omitempty"`
}

// Hysteria2Bandwidth holds bandwidth limit configuration.
type Hysteria2Bandwidth struct {
	Up   string `json:"up,omitempty"`
	Down string `json:"down,omitempty"`
}

// Hysteria2Manager manages the Hysteria2 process lifecycle.
type Hysteria2Manager struct {
	cmd        *exec.Cmd
	configPath string
	binaryPath string
	mu         sync.Mutex
	running    bool
	done       chan struct{}
}

// NewHysteria2Manager creates a new Hysteria2 process manager.
func NewHysteria2Manager(binaryPath, configPath string) *Hysteria2Manager {
	if binaryPath == "" {
		binaryPath = DefaultHysteria2BinaryPath
	}
	if configPath == "" {
		configPath = DefaultHysteria2ConfigPath
	}
	return &Hysteria2Manager{
		binaryPath: binaryPath,
		configPath: configPath,
	}
}

// Start launches the Hysteria2 process.
func (m *Hysteria2Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("hysteria2 is already running")
	}

	if _, err := os.Stat(m.binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("hysteria2 binary not found at %s", m.binaryPath)
	}

	m.cmd = exec.Command(m.binaryPath, "server", "--config", m.configPath)
	m.cmd.Stdout = newPrefixWriter("hysteria2")
	m.cmd.Stderr = newPrefixWriter("hysteria2")

	if err := m.cmd.Start(); err != nil {
		return fmt.Errorf("start hysteria2: %w", err)
	}

	m.running = true
	m.done = make(chan struct{})

	go m.waitForExit()

	return nil
}

// Stop sends SIGTERM to the Hysteria2 process and waits for it to exit.
// If the process does not exit within the timeout, it is killed.
func (m *Hysteria2Manager) Stop() error {
	m.mu.Lock()

	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		m.running = false
		m.mu.Unlock()
		return nil
	}

	if err := m.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		m.running = false
		m.cmd = nil
		m.mu.Unlock()
		return nil
	}

	done := m.done
	m.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-time.After(hysteria2StopTimeout):
		m.mu.Lock()
		if m.cmd != nil && m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
		m.mu.Unlock()
		<-done
		return fmt.Errorf("hysteria2 did not exit gracefully, killed")
	}
}

// Restart stops and starts the Hysteria2 process.
func (m *Hysteria2Manager) Restart() error {
	if err := m.Stop(); err != nil {
		return fmt.Errorf("restart stop: %w", err)
	}
	return m.Start()
}

// IsRunning reports whether the Hysteria2 process is currently running.
func (m *Hysteria2Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// UpdateConfig writes the given config to disk and restarts the process.
func (m *Hysteria2Manager) UpdateConfig(cfg Hysteria2Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hysteria2 config: %w", err)
	}

	if err := m.writeConfig(data); err != nil {
		return err
	}

	if m.IsRunning() {
		return m.Restart()
	}
	return nil
}

func (m *Hysteria2Manager) writeConfig(data []byte) error {
	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(m.configPath, data, 0o644); err != nil {
		return fmt.Errorf("write hysteria2 config: %w", err)
	}

	return nil
}

func (m *Hysteria2Manager) waitForExit() {
	err := m.cmd.Wait()

	m.mu.Lock()
	m.running = false
	m.cmd = nil
	m.mu.Unlock()

	close(m.done)

	if err != nil {
		log.Printf("[hysteria2] process exited with error: %v", err)
	} else {
		log.Printf("[hysteria2] process exited normally")
	}
}

type prefixWriter struct {
	prefix string
}

func newPrefixWriter(prefix string) *prefixWriter {
	return &prefixWriter{prefix: prefix}
}

func (w *prefixWriter) Write(p []byte) (n int, err error) {
	log.Printf("[%s] %s", w.prefix, string(p))
	return len(p), nil
}
