package historytests

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type measuredConn struct {
	net.Conn
	upload   atomic.Uint64
	download atomic.Uint64
}

func (c *measuredConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.upload.Add(uint64(n))
	return n, err
}

func (c *measuredConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.download.Add(uint64(n))
	return n, err
}

func tlsFixture(t *testing.T) (int, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var workers sync.WaitGroup
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer c.Close()
				if err := c.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
					return
				}
				if err := c.(*tls.Conn).Handshake(); err != nil {
					return
				}
				if _, err := c.Write([]byte{0x42}); err != nil {
					return
				}
				if _, err := io.Copy(c, c); err != nil {
					return
				}
			}()
		}
	}()
	t.Cleanup(func() { l.Close(); <-done; workers.Wait() })
	return l.Addr().(*net.TCPAddr).Port, certPEM, keyPEM
}

func visionConfig(ingress, client int, outbound bool, cert, key string) string {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(config("vless", ingress, client, outbound)), &cfg); err != nil {
		panic(err)
	}
	cfg["log"] = map[string]any{"loglevel": "debug"}
	var endpoint map[string]any
	tlsSettings := map[string]any{"minVersion": "1.3"}
	if outbound {
		endpoint = cfg["outbounds"].([]any)[0].(map[string]any)
		user := endpoint["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)
		user["flow"] = "xtls-rprx-vision"
		block, _ := pem.Decode([]byte(cert))
		pin := sha256.Sum256(block.Bytes)
		tlsSettings["serverName"], tlsSettings["pinnedPeerCertSha256"] = "localhost", hex.EncodeToString(pin[:])
	} else {
		endpoint = cfg["inbounds"].([]any)[0].(map[string]any)
		endpoint["settings"].(map[string]any)["clients"].([]any)[0].(map[string]any)["flow"] = "xtls-rprx-vision"
		tlsSettings["certificates"] = []any{map[string]any{"certificate": strings.Split(cert, "\n"), "key": strings.Split(key, "\n")}}
	}
	endpoint["streamSettings"] = map[string]any{"network": "raw", "security": "tls", "tlsSettings": tlsSettings}
	wire, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return string(wire)
}

func TestVisionDirectCopyCountsInnerTLSBytes(t *testing.T) {
	// Given actual TLS 1.3 inside a VLESS Vision TLS 1.3 tunnel.
	target, cert, key := tlsFixture(t)
	ingress, client := freePort(t), freePort(t)
	socket := socketPath(t)
	server := startRuntime(t, visionConfig(ingress, 0, false, cert, key), socket)
	c := connectCollector(t, socket)
	clientRuntime := startRuntime(t, visionConfig(ingress, client, true, cert, key), "")
	tcp := socksConnect(t, client, target)
	measured := &measuredConn{Conn: tcp}
	inner := tls.Client(measured, &tls.Config{InsecureSkipVerify: true, ServerName: "localhost", MinVersion: tls.VersionTLS13})
	if err := inner.Handshake(); err != nil {
		t.Fatal(err)
	}
	greeting := make([]byte, 1)
	if _, err := io.ReadFull(inner, greeting); err != nil {
		t.Fatal(err)
	}
	if greeting[0] != 0x42 {
		t.Fatal("TLS handshake completion greeting changed")
	}
	// A complete small application record exercises both direct transitions
	// before large records are split across transport buffers.
	transfer(t, inner, 32)
	for _, runtime := range []*runtimeFixture{server, clientRuntime} {
		select {
		case <-runtime.direct:
		case <-time.After(5 * time.Second):
			t.Fatal("Vision direct transition was not exercised")
		}
	}
	// Exercise direct mode while OFF first: a later enable cannot recover a
	// link that was already bypassed by splice.
	offStarted := time.Now()
	transfer(t, inner, 1024*1024)
	t.Logf("Vision OFF payload=%d roundtrip=%s", 1024*1024, time.Since(offStarted))
	baseline := c.apply(t, 1, true, epoch, "vless")
	partial := false
	for _, e := range baseline {
		if e.Kind == "flow_start" {
			partial = e.Lifecycle.PartialStart && e.Lifecycle.StartedAt == nil && *e.Bytes.Upload == 0 && *e.Bytes.Download == 0
		}
	}
	if !partial {
		t.Fatal("Vision enable omitted partial zero-baseline owner")
	}
	baseUpload, baseDownload := measured.upload.Load(), measured.download.Load()
	// When Vision switches to raw TLS records, history still observes the full
	// proxy payload boundary (TLS records, not decrypted application bytes).
	started := time.Now()
	const payload = 1024 * 1024
	transfer(t, inner, payload)
	t.Logf("Vision payload=%d roundtrip=%s bytes_per_second=%.0f", payload, time.Since(started), float64(payload)/time.Since(started).Seconds())
	if err := inner.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, inner); err != nil {
		t.Fatal(err)
	}
	if err := tcp.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, measured); err != nil {
		t.Fatal(err)
	}
	// Then no raw-copy bypass can omit bytes or double count TLS framing.
	c.endBytesWithStarts(t, measured.upload.Load()-baseUpload, measured.download.Load()-baseDownload, "vless", 0)
}
