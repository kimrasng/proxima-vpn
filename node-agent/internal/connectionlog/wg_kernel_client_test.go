//go:build linux

package connectionlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type wgKernelRequest struct {
	Action, Address, Payload string
	Packets                  [][]byte
}
type wgKernelReply struct {
	Port  int
	Error string
}

// The helper runs inside a peer namespace, using real TCP/UDP sockets.
func TestWGKernelClient(t *testing.T) {
	if os.Getenv("WG_KERNEL_CLIENT") != "1" {
		t.Skip("namespace helper")
	}
	var conn *net.TCPConn
	scan := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scan.Scan() {
		var q wgKernelRequest
		if err := json.Unmarshal(scan.Bytes(), &q); err != nil {
			t.Fatal(err)
		}
		r := wgKernelReply{}
		err := func() error {
			switch q.Action {
			case "open":
				c, err := net.DialTimeout("tcp", q.Address, 5*time.Second)
				if err != nil {
					return err
				}
				conn = c.(*net.TCPConn)
				r.Port = conn.LocalAddr().(*net.TCPAddr).Port
			case "write":
				if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return err
				}
				if _, err := io.WriteString(conn, q.Payload); err != nil {
					return err
				}
				b := make([]byte, len(q.Payload))
				if _, err := io.ReadFull(conn, b); err != nil {
					return err
				}
				if string(b) != q.Payload {
					return fmt.Errorf("echo mismatch")
				}
			case "fin":
				if err := conn.CloseWrite(); err != nil {
					return err
				}
				if _, err := io.ReadAll(conn); err != nil {
					return err
				}
				return conn.Close()
			case "rst":
				if err := conn.SetLinger(0); err != nil {
					return err
				}
				return conn.Close()
			case "udp":
				c, err := net.DialTimeout("udp", q.Address, 5*time.Second)
				if err != nil {
					return err
				}
				defer c.Close()
				r.Port = c.LocalAddr().(*net.UDPAddr).Port
				if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return err
				}
				if _, err := io.WriteString(c, q.Payload); err != nil {
					return err
				}
				b := make([]byte, len(q.Payload)+1)
				n, err := c.Read(b)
				if err != nil {
					return err
				}
				if string(b[:n]) != q.Payload {
					return fmt.Errorf("UDP echo mismatch")
				}
			case "raw":
				reply, err := net.ListenPacket("udp", "10.88.0.2:40000")
				if err != nil {
					return err
				}
				defer reply.Close()
				if err := reply.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return err
				}
				fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.IPPROTO_RAW)
				if err != nil {
					return err
				}
				defer unix.Close(fd)
				for _, b := range q.Packets {
					if err := unix.Sendto(fd, b, 0, &unix.SockaddrInet4{Addr: [4]byte{10, 88, 0, 1}}); err != nil {
						return err
					}
				}
				buf := make([]byte, 64)
				n, _, err := reply.ReadFrom(buf)
				if err != nil {
					return err
				}
				if n != 16 {
					return fmt.Errorf("fragment echo length %d", n)
				}
			case "flood":
				c, err := net.DialTimeout("udp", q.Address, 5*time.Second)
				if err != nil {
					return err
				}
				defer c.Close()
				payload := make([]byte, 1400)
				for i := 0; i < 4096; i++ {
					if _, err := c.Write(payload); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unknown helper action")
			}
			return nil
		}()
		if err != nil {
			r.Error = err.Error()
		}
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
}
