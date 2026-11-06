package historytests

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	identity   = "00000000-0000-4000-8000-000000000001"
	credential = "33333333-3333-4333-8333-333333333333"
	epoch      = "22222222-2222-4222-8222-222222222222"
)

type runtimeFixture struct {
	cmd    *exec.Cmd
	exited chan error
	direct chan struct{}
	killed bool
}

func startRuntime(t *testing.T, config string, socket string) *runtimeFixture {
	t.Helper()
	binary := os.Getenv("XRAY_HISTORY_BINARY")
	if binary == "" {
		t.Fatal("XRAY_HISTORY_BINARY must name the patched executable")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 60*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, "run", "-config", "stdin:")
	cmd.Stdin = strings.NewReader(config)
	cmd.Env = append(os.Environ(), "PROXIMA_HISTORY_SOCKET="+socket)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	ready := make(chan struct{})
	drained := make(chan struct{})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := &runtimeFixture{cmd: cmd, exited: make(chan error, 1), direct: make(chan struct{})}
	go func() {
		defer close(drained)
		scanner := bufio.NewScanner(output)
		signaled := false
		direct := false
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("runtime %s", line)
			if strings.Contains(line, "history observation:") || strings.HasPrefix(line, "panic:") {
				t.Errorf("%s", line)
			}
			if strings.Contains(line, "Xray 26.10.31 started") && !signaled {
				signaled = true
				close(ready)
			}
			if strings.Contains(line, "Xtls Unpadding new block") && strings.Contains(line, "command 2") && !direct {
				direct = true
				close(r.direct)
			}
		}
	}()
	go func() { <-drained; r.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		stopErr := cmd.Process.Signal(os.Interrupt)
		if stopErr != nil && !errors.Is(stopErr, os.ErrProcessDone) {
			t.Log(stopErr)
		}
		select {
		case err := <-r.exited:
			if err != nil {
				var exit *exec.ExitError
				expectedInterrupt := false
				if stopErr == nil && errors.As(err, &exit) {
					status, ok := exit.Sys().(syscall.WaitStatus)
					expectedInterrupt = ok && status.Signaled() && status.Signal() == syscall.SIGINT
				}
				if r.killed && errors.As(err, &exit) {
					status, ok := exit.Sys().(syscall.WaitStatus)
					expectedInterrupt = ok && status.Signaled() && status.Signal() == syscall.SIGKILL
				}
				if !expectedInterrupt {
					t.Errorf("runtime exit: %v", err)
				}
			}
		case <-time.After(5 * time.Second):
			if err := cmd.Process.Kill(); err != nil {
				t.Log(err)
			}
			<-r.exited
			t.Error("runtime did not stop after interrupt")
		}
	})
	select {
	case <-ready:
	case err := <-r.exited:
		r.exited <- err
		t.Fatalf("runtime exited before ready: %v", err)
	case <-ctx.Done():
		t.Fatal("runtime startup event: ", ctx.Err())
	}
	t.Logf("runtime pid=%d binary=%s", cmd.Process.Pid, binary)
	return r
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pxh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir + "/h"
}

func echoTCP(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if err := c.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
					return
				}
				_, err := io.Copy(c, c)
				if err != nil && !strings.Contains(err.Error(), "closed") {
					t.Logf("echo: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Log(err)
		}
		<-done
	})
	return l.Addr().(*net.TCPAddr).Port
}
