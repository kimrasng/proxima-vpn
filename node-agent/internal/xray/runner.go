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
	"sync"
	"syscall"
	"time"
)

const (
	DefaultBinaryPath = "/usr/local/bin/xray"
	DefaultConfigPath = "/etc/node-agent/xray-config.json"
	DefaultGRPCAddr   = "127.0.0.1:10085"

	// startupGrace is how long Start waits after spawning Xray before
	// concluding it came up successfully. Xray validates its config during
	// startup and exits non-zero on a bad one, so a process still alive after
	// this window has accepted its config. Kept short because it delays every
	// restart, but long enough for config parsing on a slow VPS.
	startupGrace = 3 * time.Second

	// stopTimeout is how long Stop waits for a graceful SIGTERM exit before
	// escalating to SIGKILL.
	stopTimeout = 10 * time.Second
)

// XrayRunner manages the Xray process lifecycle.
//
// Concurrency: the agent drives this from several goroutines (config poll,
// supervisor, xray update), so every method that touches process state takes
// mu. The exited channel lets IsRunning report liveness without reaping the
// child itself - a single owner goroutine per process calls cmd.Wait().
type XrayRunner struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	exited     chan struct{} // closed by the reaper when cmd terminates
	waitErr    error         // exit error observed by the reaper
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

// Start launches the Xray process and waits out startupGrace to confirm it
// stayed up.
//
// Xray exits non-zero when handed a config it cannot parse, so "spawned
// successfully" is not the same as "running". Previously Start returned nil as
// soon as fork/exec succeeded, which meant a broken config looked like a
// successful start and left the node silently dead. Start now reports an error
// if the process is gone by the end of the grace window, which is what lets
// callers roll back (see WriteConfig/RestoreConfig).
func (r *XrayRunner) Start() error {
	r.mu.Lock()
	if r.runningLocked() {
		r.mu.Unlock()
		return fmt.Errorf("xray is already running")
	}

	cmd := exec.Command(r.binaryPath, "-config", r.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("start xray: %w", err)
	}

	exited := make(chan struct{})
	r.cmd = cmd
	r.exited = exited
	r.waitErr = nil
	r.mu.Unlock()

	// Exactly one goroutine reaps this child. Without a Wait the process
	// becomes a zombie on exit, and a zombie still accepts signal 0 - which
	// is why liveness checks based solely on Signal(0) used to report a dead
	// Xray as running.
	go func() {
		err := cmd.Wait()
		r.mu.Lock()
		if r.exited == exited {
			r.waitErr = err
		}
		r.mu.Unlock()
		close(exited)
	}()

	select {
	case <-exited:
		r.mu.Lock()
		err := r.waitErr
		r.mu.Unlock()
		if err != nil {
			return fmt.Errorf("xray exited during startup: %w", err)
		}
		return fmt.Errorf("xray exited during startup")
	case <-time.After(startupGrace):
		return nil
	}
}

// Stop sends SIGTERM to the Xray process and waits for it to exit, escalating
// to SIGKILL after stopTimeout.
func (r *XrayRunner) Stop() error {
	r.mu.Lock()
	if !r.runningLocked() {
		r.cmd = nil
		r.exited = nil
		r.mu.Unlock()
		return nil
	}
	cmd := r.cmd
	exited := r.exited
	r.mu.Unlock()

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// The process may have died between the liveness check and here;
		// treat "no such process" as an ordinary stop rather than an error.
		if !isProcessGone(err) {
			return fmt.Errorf("send SIGTERM to xray: %w", err)
		}
	}

	var killed bool
	select {
	case <-exited:
	case <-time.After(stopTimeout):
		_ = cmd.Process.Kill()
		<-exited // the reaper always closes this once the child is gone
		killed = true
	}

	r.mu.Lock()
	if r.exited == exited {
		r.cmd = nil
		r.exited = nil
	}
	r.mu.Unlock()

	if killed {
		return fmt.Errorf("xray did not exit gracefully, killed")
	}
	return nil
}

// Restart stops and starts the Xray process.
func (r *XrayRunner) Restart() error {
	if err := r.Stop(); err != nil {
		return fmt.Errorf("restart stop: %w", err)
	}
	return r.Start()
}

// IsRunning reports whether the Xray process is currently alive.
func (r *XrayRunner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runningLocked()
}

// runningLocked reports liveness from the reaper's exited channel. Callers
// must hold mu.
func (r *XrayRunner) runningLocked() bool {
	if r.cmd == nil || r.cmd.Process == nil || r.exited == nil {
		return false
	}
	select {
	case <-r.exited:
		return false
	default:
		return true
	}
}

// isProcessGone reports whether err indicates the target process no longer
// exists.
func isProcessGone(err error) bool {
	return err == os.ErrProcessDone || strings.Contains(err.Error(), "process already finished")
}

// WriteConfig writes the Xray configuration JSON to the config path, keeping
// the previous contents in a sibling .prev file so a config that Xray refuses
// can be rolled back (see RestoreConfig).
func (r *XrayRunner) WriteConfig(data []byte) error {
	dir := filepath.Dir(r.configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if prev, err := os.ReadFile(r.configPath); err == nil {
		if err := os.WriteFile(r.backupPath(), prev, 0o644); err != nil {
			return fmt.Errorf("back up previous xray config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read previous xray config: %w", err)
	}

	// Write to a temp file and rename so a crash mid-write cannot leave a
	// truncated config that Xray would then fail to parse on next start.
	tmp, err := os.CreateTemp(dir, "xray-config-*.json")
	if err != nil {
		return fmt.Errorf("create temp xray config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write xray config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp xray config: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("chmod xray config: %w", err)
	}
	if err := os.Rename(tmpPath, r.configPath); err != nil {
		return fmt.Errorf("replace xray config: %w", err)
	}

	return nil
}

// HasBackupConfig reports whether a previous config is available to roll back
// to.
func (r *XrayRunner) HasBackupConfig() bool {
	_, err := os.Stat(r.backupPath())
	return err == nil
}

// RestoreConfig puts the previous config back in place and returns its
// contents, so a caller that failed to start Xray on a new config can revert
// and resync its change-detection hash. It does not restart Xray.
func (r *XrayRunner) RestoreConfig() ([]byte, error) {
	prev, err := os.ReadFile(r.backupPath())
	if err != nil {
		return nil, fmt.Errorf("read xray config backup: %w", err)
	}
	if err := os.WriteFile(r.configPath, prev, 0o644); err != nil {
		return nil, fmt.Errorf("restore xray config: %w", err)
	}
	return prev, nil
}

// backupPath is where WriteConfig stashes the config it is replacing.
func (r *XrayRunner) backupPath() string {
	return r.configPath + ".prev"
}

// ConfigPath returns the path Xray is configured from.
func (r *XrayRunner) ConfigPath() string {
	return r.configPath
}

// GRPCAddr returns the gRPC address for the stats API.
func (r *XrayRunner) GRPCAddr() string {
	return r.grpcAddr
}

// Version runs the xray binary with -version and returns its version string
// (e.g. "v1.8.24"), or an error if the binary is missing or unparsable.
func (r *XrayRunner) Version() (string, error) {
	return r.versionOf(r.binaryPath)
}

// versionOf runs -version against an arbitrary xray binary path. Used both for
// the installed binary and to validate a freshly downloaded one before it
// replaces the installed copy.
func (r *XrayRunner) versionOf(path string) (string, error) {
	out, err := exec.Command(path, "-version").Output()
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

// StageBinary downloads the given Xray-core release from GitHub, extracts the
// xray executable to a temp file next to the installed binary, and verifies it
// runs by executing -version against it. It returns the staged path; the
// caller is responsible for calling CommitBinary to move it into place or
// DiscardBinary to clean up.
//
// Download and validation happen while the current Xray keeps serving. The
// previous implementation stopped Xray *before* downloading, so every slow or
// failed download was a service outage of that duration.
func (r *XrayRunner) StageBinary(ctx context.Context, targetVersion string) (string, error) {
	asset, ok := xrayReleaseAssets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("no xray release asset known for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/%s", targetVersion, asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download xray release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download xray release: status %d", resp.StatusCode)
	}

	zipData, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20)) // 200MB cap
	if err != nil {
		return "", fmt.Errorf("read xray release archive: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("open xray release archive: %w", err)
	}

	var xrayBin []byte
	for _, f := range zr.File {
		if f.Name != "xray" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open xray binary in archive: %w", err)
		}
		xrayBin, err = io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return "", fmt.Errorf("read xray binary from archive: %w", err)
		}
		break
	}
	if xrayBin == nil {
		return "", fmt.Errorf("xray binary not found in release archive")
	}

	dir := filepath.Dir(r.binaryPath)
	tmpFile, err := os.CreateTemp(dir, "xray-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	staged := false
	defer func() {
		if !staged {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(xrayBin); err != nil {
		_ = tmpFile.Close()
		return "", fmt.Errorf("write xray binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod xray binary: %w", err)
	}

	// Refuse to install something that cannot even report its version - a
	// truncated or wrong-arch download would otherwise only be discovered
	// after the running Xray had already been stopped.
	if _, err := r.versionOf(tmpPath); err != nil {
		return "", fmt.Errorf("validate downloaded xray binary: %w", err)
	}

	staged = true
	return tmpPath, nil
}

// CommitBinary atomically moves a binary staged by StageBinary into place,
// keeping the outgoing binary as a sibling .prev file so CommitBinary's caller
// can roll back with RestoreBinary if the new one fails to start.
func (r *XrayRunner) CommitBinary(stagedPath string) error {
	if cur, err := os.ReadFile(r.binaryPath); err == nil {
		if err := os.WriteFile(r.binaryPath+".prev", cur, 0o755); err != nil {
			return fmt.Errorf("back up current xray binary: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read current xray binary: %w", err)
	}

	if err := os.Rename(stagedPath, r.binaryPath); err != nil {
		return fmt.Errorf("replace xray binary: %w", err)
	}
	return nil
}

// DiscardBinary removes a staged binary that will not be installed.
func (r *XrayRunner) DiscardBinary(stagedPath string) {
	_ = os.Remove(stagedPath)
}

// RestoreBinary puts the previous binary back after a failed upgrade.
func (r *XrayRunner) RestoreBinary() error {
	prev := r.binaryPath + ".prev"
	data, err := os.ReadFile(prev)
	if err != nil {
		return fmt.Errorf("read xray binary backup: %w", err)
	}
	if err := os.WriteFile(r.binaryPath, data, 0o755); err != nil {
		return fmt.Errorf("restore xray binary: %w", err)
	}
	return nil
}
