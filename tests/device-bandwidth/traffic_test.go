package bandwidth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These documentation-only IPs are never dialled: the limiter's injected dial
// seam must map them to our loopback targets or reject them.
const payloadHost = "198.51.100.10"
const payloadPort = 443

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "bandwidth.test"},
		DNSNames: []string{"bandwidth.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}
}

// payloadServer is a loopback-only TLS endpoint. Its infinite body permits a
// fixed measurement window rather than a fragile completion-time assertion.
func payloadServer(t *testing.T) string {
	t.Helper()
	return payloadServerWithGate(t, nil)
}

func payloadServerWithGate(t *testing.T, begin <-chan struct{}) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", testTLSConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	conns := map[net.Conn]struct{}{}
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns[c] = struct{}{}
			mu.Unlock()
			go func() {
				defer func() { c.Close(); mu.Lock(); delete(conns, c); mu.Unlock() }()
				_ = c.SetDeadline(time.Now().Add(45 * time.Second))
				request := make([]byte, 4096)
				if _, err := c.Read(request); err != nil {
					return
				}
				if begin != nil {
					<-begin
				}
				if _, err := io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nConnection: close\r\n\r\n"); err != nil {
					return
				}
				body := make([]byte, 16*1024)
				for {
					if _, err := c.Write(body); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func socksConnect(t *testing.T, proxy, username, password string, command byte, host string, port int) (net.Conn, *net.UDPAddr) {
	t.Helper()
	c, err := net.DialTimeout("tcp", proxy, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fail := func(err error) { c.Close(); t.Fatal(err) }
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	method := byte(0)
	if username != "" {
		method = 2
	}
	if _, err := c.Write([]byte{5, 1, method}); err != nil {
		fail(err)
	}
	r := make([]byte, 2)
	if _, err := io.ReadFull(c, r); err != nil {
		fail(err)
	}
	if r[0] != 5 || r[1] != method {
		fail(fmt.Errorf("SOCKS method rejected: %v", r))
	}
	if method == 2 {
		auth := append([]byte{1, byte(len(username))}, []byte(username)...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, []byte(password)...)
		if _, err := c.Write(auth); err != nil {
			fail(err)
		}
		if _, err := io.ReadFull(c, r); err != nil {
			fail(err)
		}
		if r[1] != 0 {
			fail(fmt.Errorf("SOCKS authentication rejected"))
		}
	}
	request := []byte{5, command, 0, 1}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		fail(fmt.Errorf("test target must be IPv4"))
	}
	request = append(request, ip...)
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := c.Write(request); err != nil {
		fail(err)
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(c, header); err != nil {
		fail(err)
	}
	if header[0] != 5 || header[1] != 0 {
		fail(fmt.Errorf("SOCKS command rejected: %v", header))
	}
	var addr []byte
	switch header[3] {
	case 1:
		addr = make([]byte, 4)
	case 4:
		addr = make([]byte, 16)
	default:
		fail(fmt.Errorf("unexpected SOCKS address type %d", header[3]))
	}
	if _, err := io.ReadFull(c, addr); err != nil {
		fail(err)
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(c, portBytes); err != nil {
		fail(err)
	}
	_ = c.SetDeadline(time.Time{})
	return c, &net.UDPAddr{IP: net.IP(addr), Port: int(binary.BigEndian.Uint16(portBytes))}
}

func openPayload(t *testing.T, proxy, username, password string) net.Conn {
	t.Helper()
	c, _ := socksConnect(t, proxy, username, password, 1, payloadHost, payloadPort)
	secure := tls.Client(c, &tls.Config{InsecureSkipVerify: true, ServerName: "bandwidth.test", MinVersion: tls.VersionTLS13}) // isolated ephemeral test certificate
	_ = secure.SetDeadline(time.Now().Add(8 * time.Second))
	if err := secure.Handshake(); err != nil {
		secure.Close()
		t.Fatal(err)
	}
	if _, err := io.WriteString(secure, "GET / HTTP/1.1\r\nHost: bandwidth.test\r\n\r\n"); err != nil {
		secure.Close()
		t.Fatal(err)
	}
	_ = secure.SetDeadline(time.Time{})
	t.Cleanup(func() { secure.Close() })
	return secure
}

type trafficResult struct {
	Bytes              int64   `json:"bytes"`
	WindowSeconds      float64 `json:"window_seconds"`
	RateBytesPerSecond int64   `json:"rate_bytes_per_second"`
	BurstAllowance     int64   `json:"burst_allowance_bytes"`
	CeilingBytes       int64   `json:"ceiling_bytes"`
}

// A monotonic common window and an explicit starting burst avoid claiming that
// short transfers prove shaping. Readers remain alive throughout the window.
func measure(t *testing.T, streams []net.Conn, window time.Duration, rate, burst int64, begin <-chan struct{}) trafficResult {
	t.Helper()
	var bytes atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	if begin != nil {
		<-begin
	}
	start := time.Now()
	deadline := start.Add(window)
	for _, c := range streams {
		_ = c.SetReadDeadline(deadline)
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			buf := make([]byte, 32*1024)
			for {
				n, err := c.Read(buf)
				bytes.Add(int64(n))
				if err != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
			}
		}(c)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	result := trafficResult{Bytes: bytes.Load(), WindowSeconds: elapsed, RateBytesPerSecond: rate, BurstAllowance: burst, CeilingBytes: int64(float64(rate)*elapsed) + burst}
	if elapsed < window.Seconds()*0.9 {
		t.Fatalf("streams closed before full measurement window: %+v", result)
	}
	if result.Bytes > result.CeilingBytes {
		t.Errorf("aggregate bandwidth exceeded ceiling: %+v", result)
	}
	if result.Bytes < int64(float64(rate)*elapsed*0.65) {
		t.Errorf("insufficient throughput / stalled forwarding: %+v", result)
	}
	return result
}
