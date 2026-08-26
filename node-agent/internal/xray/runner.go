package xray

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// Version runs the xray binary with -version and returns its version string
// (e.g. "v1.8.24"), or an error if the binary is missing or unparsable.
func (r *XrayRunner) Version() (string, error) {
	out, err := exec.Command(r.binaryPath, "-version").Output()
	if err != nil {
		return "", fmt.Errorf("run xray -version: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", fmt.Errorf("unexpected xray -version output: %q", string(out))
	}
	return "v" + fields[1], nil
}

// xrayReleaseAssets maps GOOS/GOARCH to the Xray-core release zip asset name.
// See https://github.com/XTLS/Xray-core/releases.
var xrayReleaseAssets = map[string]string{
	"linux/amd64": "Xray-linux-64.zip",
	"linux/arm64": "Xray-linux-arm64-v8a.zip",
}

// UpdateBinary downloads the given Xray-core release from GitHub, extracts the
// xray executable, and atomically replaces the current binary. The caller is
// responsible for stopping the process beforehand and starting it again after
// a successful update (see Restart).
func (r *XrayRunner) UpdateBinary(ctx context.Context, targetVersion string) error {
	asset, ok := xrayReleaseAssets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return fmt.Errorf("no xray release asset known for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/%s", targetVersion, asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download xray release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download xray release: status %d", resp.StatusCode)
	}

	zipData, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20)) // 200MB cap
	if err != nil {
		return fmt.Errorf("read xray release archive: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("open xray release archive: %w", err)
	}

	var xrayBin []byte
	for _, f := range zr.File {
		if f.Name != "xray" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open xray binary in archive: %w", err)
		}
		xrayBin, err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("read xray binary from archive: %w", err)
		}
		break
	}
	if xrayBin == nil {
		return fmt.Errorf("xray binary not found in release archive")
	}

	dir := filepath.Dir(r.binaryPath)
	tmpFile, err := os.CreateTemp(dir, "xray-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(xrayBin); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write xray binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod xray binary: %w", err)
	}
	if err := os.Rename(tmpPath, r.binaryPath); err != nil {
		return fmt.Errorf("replace xray binary: %w", err)
	}

	success = true
	return nil
}
