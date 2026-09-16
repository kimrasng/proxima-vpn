package xray

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeXray writes a shell script standing in for the xray binary, so the
// process-lifecycle tests exercise real fork/exec/signal behaviour rather than
// a mock. body runs with "$@" holding the flags XrayRunner passes.
func fakeXray(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake xray: %v", err)
	}
	return path
}

func newTestRunner(t *testing.T, binary string) *XrayRunner {
	t.Helper()
	r := NewXrayRunner(filepath.Join(t.TempDir(), "config.json"), "")
	r.binaryPath = binary
	t.Cleanup(func() { _ = r.Stop() })
	return r
}

func TestStartReportsLongRunningProcessAsRunning(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "sleep 30"))

	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !r.IsRunning() {
		t.Error("IsRunning() = false for a live process")
	}
	if err := r.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if r.IsRunning() {
		t.Error("IsRunning() = true after Stop")
	}
}

// A config Xray refuses makes it exit almost immediately. Start must surface
// that instead of reporting success, since callers roll back on the error.
func TestStartFailsWhenProcessExitsImmediately(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "exit 23"))

	if err := r.Start(); err == nil {
		t.Fatal("Start() = nil for a process that exits at once, want error")
	}
	if r.IsRunning() {
		t.Error("IsRunning() = true after a failed start")
	}
}

// The regression this guards: the old runner called cmd.Process.Wait() only
// inside Stop, so a process that died on its own became a zombie, and a zombie
// still accepts signal 0 - making IsRunning report a dead Xray as alive and
// leaving nothing to trigger a restart.
func TestIsRunningDetectsProcessThatDiedOnItsOwn(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "sleep 0.2; exit 1"))

	if err := r.Start(); err != nil {
		// A 0.2s lifetime is shorter than startupGrace, so Start correctly
		// reports failure; the point here is what IsRunning says afterwards.
		t.Logf("Start returned %v (expected for a short-lived process)", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for r.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if r.IsRunning() {
		t.Fatal("IsRunning() = true for a process that exited on its own (zombie not reaped)")
	}
}

func TestStopIsIdempotentWhenNotRunning(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "sleep 30"))

	if err := r.Stop(); err != nil {
		t.Errorf("Stop() on a never-started runner: %v", err)
	}
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Stop(); err != nil {
		t.Errorf("first Stop: %v", err)
	}
	if err := r.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestStartRejectsDoubleStart(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "sleep 30"))

	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Start(); err == nil {
		t.Error("second Start() = nil, want error")
	}
}

// Stop must escalate to SIGKILL for a process that ignores SIGTERM, and must
// not hang waiting for it.
func TestStopKillsProcessIgnoringSIGTERM(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "trap '' TERM; sleep 60"))

	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- r.Stop() }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Stop() = nil for a SIGTERM-ignoring process, want the killed error")
		}
	case <-time.After(stopTimeout + 10*time.Second):
		t.Fatal("Stop() hung on a SIGTERM-ignoring process")
	}

	if r.IsRunning() {
		t.Error("IsRunning() = true after Stop killed the process")
	}
}

func TestWriteConfigBacksUpAndRestores(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "sleep 30"))

	if r.HasBackupConfig() {
		t.Fatal("HasBackupConfig() = true before any write")
	}
	if err := r.WriteConfig([]byte(`{"v":1}`)); err != nil {
		t.Fatalf("first WriteConfig: %v", err)
	}
	// Nothing to back up on the first write, so still no backup.
	if r.HasBackupConfig() {
		t.Error("HasBackupConfig() = true after the first write")
	}

	if err := r.WriteConfig([]byte(`{"v":2}`)); err != nil {
		t.Fatalf("second WriteConfig: %v", err)
	}
	if !r.HasBackupConfig() {
		t.Fatal("HasBackupConfig() = false after overwriting a config")
	}

	current, err := os.ReadFile(r.ConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(current) != `{"v":2}` {
		t.Errorf("config = %q, want the newest write", current)
	}

	restored, err := r.RestoreConfig()
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}
	if string(restored) != `{"v":1}` {
		t.Errorf("RestoreConfig returned %q, want the previous config", restored)
	}

	onDisk, err := os.ReadFile(r.ConfigPath())
	if err != nil {
		t.Fatalf("read config after restore: %v", err)
	}
	if string(onDisk) != `{"v":1}` {
		t.Errorf("config on disk = %q, want the previous config", onDisk)
	}
}

func TestRestoreConfigFailsWithoutBackup(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "sleep 30"))

	if _, err := r.RestoreConfig(); err == nil {
		t.Error("RestoreConfig() = nil error with no backup present")
	}
}

func TestStageBinaryRejectsUnusableDownload(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "sleep 30"))

	// No network in tests: an unreachable release URL is enough to prove
	// StageBinary reports failure without touching the installed binary.
	if _, err := r.StageBinary(t.Context(), "v0.0.0-does-not-exist"); err == nil {
		t.Error("StageBinary() = nil error for a nonexistent release")
	}

	if _, err := os.Stat(r.binaryPath); err != nil {
		t.Errorf("installed binary disturbed by a failed StageBinary: %v", err)
	}
}

func TestCommitBinaryKeepsPreviousForRollback(t *testing.T) {
	r := newTestRunner(t, fakeXray(t, "echo old"))

	staged := filepath.Join(filepath.Dir(r.binaryPath), "staged-xray")
	if err := os.WriteFile(staged, []byte("#!/bin/sh\necho new\n"), 0o755); err != nil {
		t.Fatalf("write staged binary: %v", err)
	}

	if err := r.CommitBinary(staged); err != nil {
		t.Fatalf("CommitBinary: %v", err)
	}

	installed, err := os.ReadFile(r.binaryPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(installed) != "#!/bin/sh\necho new\n" {
		t.Error("CommitBinary did not install the staged binary")
	}

	if err := r.RestoreBinary(); err != nil {
		t.Fatalf("RestoreBinary: %v", err)
	}
	rolled, err := os.ReadFile(r.binaryPath)
	if err != nil {
		t.Fatalf("read rolled-back binary: %v", err)
	}
	if string(rolled) != "#!/bin/sh\necho old\n" {
		t.Errorf("RestoreBinary left %q, want the original binary", rolled)
	}
}
