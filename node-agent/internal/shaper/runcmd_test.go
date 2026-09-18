package shaper

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// tc refuses rules without CAP_NET_ADMIN by printing to stderr and exiting 0,
// so a check on exit status alone reports a refused rule as applied - which is
// how speed limits could be silently absent on a node that looked healthy.
func TestStderrOutputIsAFailureEvenWhenTheCommandExitsZero(t *testing.T) {
	original := runCmd
	t.Cleanup(func() { runCmd = original })

	err := runCmd("sh", "-c", "echo 'RTNETLINK answers: Operation not permitted' >&2; exit 0")
	if err == nil {
		t.Fatal("a command that wrote to stderr and exited 0 was treated as success")
	}
	if !strings.Contains(err.Error(), "Operation not permitted") {
		t.Errorf("error should carry what the command reported, got: %v", err)
	}
}

// tc warns about htb quantum sizing on rules it installs correctly. Treating
// that as failure would report working shaping as broken.
func TestAdvisoryWarningsAreNotFailures(t *testing.T) {
	err := runCmd("sh", "-c", "echo 'Warning: sch_htb: quantum of class 10001 is big.' >&2; exit 0")
	if err != nil {
		t.Fatalf("an advisory warning was treated as failure: %v", err)
	}
}

// A real error alongside a warning must still surface.
func TestErrorAlongsideWarningIsStillAFailure(t *testing.T) {
	err := runCmd("sh", "-c", "echo 'Warning: something advisory' >&2; echo 'RTNETLINK answers: Operation not permitted' >&2; exit 0")
	if err == nil {
		t.Fatal("a real error was swallowed because a warning accompanied it")
	}
	if strings.Contains(err.Error(), "advisory") {
		t.Errorf("warning text leaked into the error: %v", err)
	}
}

func TestSilentSuccessIsStillSuccess(t *testing.T) {
	if err := runCmd("sh", "-c", "exit 0"); err != nil {
		t.Fatalf("a clean command was rejected: %v", err)
	}
}

func TestNonZeroExitIsAFailure(t *testing.T) {
	if err := runCmd("sh", "-c", "exit 3"); err == nil {
		t.Fatal("a command exiting 3 was treated as success")
	}
}

// Proves the real tc binary behaves the way the stderr check assumes. Skipped
// where tc is absent or the caller already holds CAP_NET_ADMIN, since then the
// command legitimately succeeds.
func TestRealTcRefusalIsDetected(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("tc is Linux-only")
	}
	if _, err := exec.LookPath("tc"); err != nil {
		t.Skip("tc not installed")
	}

	err := runCmd("tc", "qdisc", "add", "dev", "lo", "root", "handle", "1:", "htb", "default", "30")
	if err == nil {
		// Privileged: undo it so the test leaves nothing behind.
		_ = runCmd("tc", "qdisc", "del", "dev", "lo", "root")
		t.Skip("running with CAP_NET_ADMIN; refusal path not exercised")
	}
	if !strings.Contains(err.Error(), "tc") {
		t.Errorf("failure should name the command, got: %v", err)
	}
}
