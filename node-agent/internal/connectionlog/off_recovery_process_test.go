package connectionlog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/proximavpn/proxima-vpn/node-agent/internal/client"
	"github.com/proximavpn/proxima-vpn/node-agent/internal/config"
)

type offRecoveryReply struct {
	Status Status `json:"status"`
	Error  string `json:"error"`
}

// The API integration invokes this test executable, rather than importing an
// agent internal package across Go's module boundary.
func TestHistoryOFFRecoveryAgentProcess(t *testing.T) {
	if os.Getenv("HISTORY_B1_PROCESS") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	api := client.NewAPIClient(&config.AgentConfig{ServerURL: os.Getenv("HISTORY_B1_SERVER"),
		NodeID: os.Getenv("HISTORY_B1_NODE"), APIKey: "b1-key"})
	m, err := NewManager(Options{Directory: os.Getenv("HISTORY_B1_DIRECTORY"), API: api})
	must(t, err)
	defer func() { must(t, m.Close()) }()
	must(t, m.Register(NewUnixSource("xray", os.Getenv("HISTORY_B1_SOCKET"))))
	must(t, m.Poll(ctx))
	sourceCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.workerError(m.workers["xray"], m.collect(sourceCtx, m.workers["xray"]))
	}()
	defer func() { stop(); <-done }()
	applied := func() error {
		for {
			changed := m.Changes()
			s := m.Status()
			if s.AppliedRevision == s.DesiredRevision && len(s.Producers) == 1 && s.Producers[0].ErrorCode == "" {
				return nil
			}
			select {
			case <-done:
				return fmt.Errorf("source stopped: %s", m.Status().ErrorCode)
			case <-changed:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	reply := func(err error) {
		r := offRecoveryReply{Status: m.Status()}
		if err != nil {
			r.Error = err.Error()
		}
		raw, err := json.Marshal(r)
		must(t, err)
		fmt.Printf("B1_REPLY %s\n", raw)
	}
	reply(applied())
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "poll":
			err := m.Poll(ctx)
			if err == nil {
				err = applied()
			}
			reply(err)
		case "upload":
			reply(m.UploadOnce(ctx))
		case "quit":
			return
		default:
			t.Fatalf("unknown command %q", scanner.Text())
		}
	}
	must(t, scanner.Err())
}

type offRecoveryProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	replies <-chan offRecoveryReply
	done    <-chan error
	joined  bool
}

func startOFFRecoveryProcess(t *testing.T, ctx context.Context, dir, socket string) *offRecoveryProcess {
	t.Helper()
	executable, err := os.Executable()
	must(t, err)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestHistoryOFFRecoveryAgentProcess$")
	cmd.Env = append(os.Environ(), "HISTORY_B1_PROCESS=1", "HISTORY_B1_DIRECTORY="+dir, "HISTORY_B1_SOCKET="+socket)
	input, err := cmd.StdinPipe()
	must(t, err)
	output, err := cmd.StdoutPipe()
	must(t, err)
	cmd.Stderr = cmd.Stdout
	replies := make(chan offRecoveryReply, 1)
	scanned := make(chan error, 1)
	done := make(chan error, 1)
	p := &offRecoveryProcess{command: cmd, input: input, replies: replies, done: done}
	must(t, cmd.Start())
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "B1_REPLY ") {
				continue
			}
			var reply offRecoveryReply
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "B1_REPLY ")), &reply); err != nil {
				scanned <- err
				return
			}
			select {
			case replies <- reply:
			case <-ctx.Done():
				scanned <- ctx.Err()
				return
			}
		}
		scanned <- scanner.Err()
	}()
	go func() {
		scanErr := <-scanned
		err := cmd.Wait()
		if scanErr != nil {
			err = scanErr
		}
		done <- err
	}()
	t.Logf("B1_AGENT pid=%d", cmd.Process.Pid)
	t.Cleanup(func() {
		if !p.joined {
			if err := cmd.Process.Kill(); err != nil {
				t.Errorf("kill agent: %v", err)
			}
			<-done
		}
		input.Close()
		t.Logf("CLEANUP agent pid=%d joined", cmd.Process.Pid)
	})
	return p
}

func (p *offRecoveryProcess) receive(t *testing.T, ctx context.Context) Status {
	t.Helper()
	select {
	case reply := <-p.replies:
		if reply.Error != "" {
			t.Fatalf("agent command: %s status=%+v", reply.Error, reply.Status)
		}
		return reply.Status
	case err := <-p.done:
		p.joined = true
		t.Fatalf("agent exited before reply: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return Status{}
}

func (p *offRecoveryProcess) request(t *testing.T, ctx context.Context, command string) Status {
	t.Helper()
	_, err := fmt.Fprintln(p.input, command)
	must(t, err)
	return p.receive(t, ctx)
}

func (p *offRecoveryProcess) kill(t *testing.T) {
	t.Helper()
	must(t, p.command.Process.Kill())
	if err := <-p.done; err == nil {
		t.Fatal("SIGKILL did not terminate agent")
	}
	p.joined = true
}
