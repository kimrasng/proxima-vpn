package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

const dockerBinary = "/usr/bin/docker"
const composePluginPath = "/usr/local/libexec/docker/cli-plugins/docker-compose"

var errUncertain = errors.New("command outcome uncertain")

type Executor interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type limitedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		b.exceeded = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

type commandExecutor struct{ dir string }

func (e commandExecutor) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = e.dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + e.dir, "DOCKER_CONFIG=" + e.dir + "/docker-config",
		"DOCKER_HOST=unix:///var/run/docker.sock", "COMPOSE_ANSI=never"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.Process != nil {
		if killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
			return nil, fmt.Errorf("%w: process group cleanup failed", errUncertain)
		}
	}
	if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) || out.exceeded {
		return nil, errUncertain
	}
	if err != nil {
		// Never return process output: inspect/Compose errors can include secrets.
		return nil, fmt.Errorf("command failed: %w", err)
	}
	return out.Bytes(), nil
}
