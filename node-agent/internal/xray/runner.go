package xray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	DefaultBinaryPath = "/usr/local/bin/xray"
	DefaultConfigPath = "/etc/node-agent/xray-config.json"
	DefaultGRPCAddr   = "127.0.0.1:10085"
)

// XrayRunner manages the Xray process lifecycle.
type XrayRunner struct {
	cmd        *exec.Cmd
	configPath string
	grpcAddr   string
	binaryPath string
}

// NewXrayRunner creates a new Xray process runner.
func NewXrayRunner(configPath, grpcAddr string) *XrayRunner {
	if configPath == "" {
		configPath = DefaultConfigPath
	}
	if grpcAddr == "" {
		grpcAddr = DefaultGRPCAddr
	}
	return &XrayRunner{
		configPath: configPath,
		grpcAddr:   grpcAddr,
		binaryPath: DefaultBinaryPath,
	}
}

// Start launches the Xray process.
func (r *XrayRunner) Start() error {
	if r.IsRunning() {
		return fmt.Errorf("xray is already running")
	}

	r.cmd = exec.Command(r.binaryPath, "-config", r.configPath)
	r.cmd.Stdout = os.Stdout
	r.cmd.Stderr = os.Stderr

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("start xray: %w", err)
	}

	return nil
}

// Stop sends SIGTERM to the Xray process and waits for it to exit.
func (r *XrayRunner) Stop() error {
	if !r.IsRunning() {
		return nil
	}

	if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to xray: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := r.cmd.Process.Wait()
		done <- err
	}()

	select {
	case <-done:
		r.cmd = nil
		return nil
	case <-time.After(10 * time.Second):
		_ = r.cmd.Process.Kill()
		r.cmd = nil
		return fmt.Errorf("xray did not exit gracefully, killed")
	}
}

// Restart stops and starts the Xray process.
func (r *XrayRunner) Restart() error {
	if err := r.Stop(); err != nil {
		return fmt.Errorf("restart stop: %w", err)
	}
	return r.Start()
}

// IsRunning checks if the Xray process is currently running.
func (r *XrayRunner) IsRunning() bool {
	if r.cmd == nil || r.cmd.Process == nil {
		return false
	}
	// Check if process is still alive
	err := r.cmd.Process.Signal(syscall.Signal(0))
	return err == nil
}

// WriteConfig writes the Xray configuration JSON to the config path.
func (r *XrayRunner) WriteConfig(data []byte) error {
	dir := filepath.Dir(r.configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(r.configPath, data, 0o644); err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}

	return nil
}

// GRPCAddr returns the gRPC address for the stats API.
func (r *XrayRunner) GRPCAddr() string {
	return r.grpcAddr
}
