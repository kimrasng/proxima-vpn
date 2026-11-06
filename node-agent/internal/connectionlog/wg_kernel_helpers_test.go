//go:build linux

package connectionlog

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
)

type wgKernelClient struct {
	input  io.WriteCloser
	encode *json.Encoder
	decode *json.Decoder
	cmd    *exec.Cmd
}

func wgKernelCommand(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	t.Logf("COMMAND %q", args)
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v: %s", args, err, out)
	}
	return string(out)
}

func wgStartClient(t *testing.T, ctx context.Context, namespace string) *wgKernelClient {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", namespace, exe, "-test.run=^TestWGKernelClient$")
	cmd.Env = append(os.Environ(), "WG_KERNEL_CLIENT=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &wgKernelClient{input: in, encode: json.NewEncoder(in), decode: json.NewDecoder(out), cmd: cmd}
	t.Cleanup(func() {
		if err := in.Close(); err != nil {
			t.Error(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func (c *wgKernelClient) do(t *testing.T, q wgKernelRequest) wgKernelReply {
	t.Helper()
	if err := c.encode.Encode(q); err != nil {
		t.Fatal(err)
	}
	var r wgKernelReply
	if err := c.decode.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	return r
}

func wgEcho(t *testing.T, network, address string) string {
	t.Helper()
	if network == "udp" {
		c, err := net.ListenPacket("udp", address)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			b := make([]byte, 65535)
			for {
				n, a, err := c.ReadFrom(b)
				if err != nil {
					return
				}
				if _, err := c.WriteTo(b[:n], a); err != nil {
					return
				}
			}
		}()
		t.Cleanup(func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
			<-done
		})
		return c.LocalAddr().String()
	}
	l, err := net.Listen("tcp", address)
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
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	return l.Addr().String()
}
