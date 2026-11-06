package subscription_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type historyLiveProcess struct {
	cmd     *exec.Cmd
	done    chan error
	mu      sync.Mutex
	lines   []string
	changed chan struct{}
}

func historyLiveCommand(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v: %s", args[0], err, out)
	}
}

func historyLiveStart(t *testing.T, dir, name string, env []string, args ...string) *historyLiveProcess {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(dir, name+".log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = writer, writer
	p := &historyLiveProcess{cmd: cmd, done: make(chan error, 1), changed: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer reader.Close()
		defer logFile.Close()
		scanner := bufio.NewScanner(io.TeeReader(reader, logFile))
		for scanner.Scan() {
			p.mu.Lock()
			p.lines = append(p.lines, scanner.Text())
			close(p.changed)
			p.changed = make(chan struct{})
			p.mu.Unlock()
		}
	}()
	go func() { err := cmd.Wait(); <-readDone; p.done <- err }()
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-p.done
		}
		t.Logf("CLEANUP %s pid=%d stopped", name, cmd.Process.Pid)
	})
	return p
}

func (p *historyLiveProcess) mark() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.lines)
}

func (p *historyLiveProcess) wait(t *testing.T, ctx context.Context, after int, contains string) int {
	t.Helper()
	for {
		p.mu.Lock()
		for i := after; i < len(p.lines); i++ {
			if strings.Contains(p.lines[i], contains) {
				p.mu.Unlock()
				return i + 1
			}
		}
		after = len(p.lines)
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("waiting for %q: %v", contains, ctx.Err())
		}
	}
}

func historyLiveJSON(t *testing.T, base, method, path, token, key string, payload any, want int, result any) {
	t.Helper()
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("X-Node-Key", key)
	}
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("HTTP %s %s status=%d bytes=%d", method, path, resp.StatusCode, len(data))
	if resp.StatusCode != want {
		t.Fatalf("HTTP status=%d body=%s", resp.StatusCode, data)
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			t.Fatal(err)
		}
	}
}

func historyLivePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func historyLiveWrite(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func historyLiveSOCKS(t *testing.T, proxy, target string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	conn.Write([]byte{5, 1, 0})
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || !bytes.Equal(reply, []byte{5, 0}) {
		t.Fatalf("SOCKS greeting: %v %v", reply, err)
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	fmt.Sscan(portText, &port)
	request := append([]byte{5, 1, 0, 1}, net.ParseIP(host).To4()...)
	request = append(request, byte(port>>8), byte(port))
	conn.Write(request)
	response := make([]byte, 10)
	if _, err := io.ReadFull(conn, response); err != nil || response[1] != 0 {
		t.Fatalf("SOCKS connect: %v %v", response, err)
	}
	conn.SetDeadline(time.Time{})
	return conn
}
