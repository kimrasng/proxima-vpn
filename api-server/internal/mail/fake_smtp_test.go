package mail

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal in-process SMTP server that records what a client does.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	tlsCfg   *tls.Config
	starttls bool // advertise STARTTLS
	implicit bool // wrap every connection in TLS from the first byte
	auth     bool // advertise AUTH PLAIN

	mu       sync.Mutex
	verbs    []string
	authN    int
	authOver []bool // whether each AUTH arrived on a TLS connection
	mails    []capturedMail
	wg       sync.WaitGroup
}

type capturedMail struct {
	from  string
	rcpts []string
	data  string
}

func newFakeSMTP(t *testing.T, opts func(*fakeSMTP)) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{t: t, ln: ln}
	if opts != nil {
		opts(f)
	}
	f.wg.Add(1)
	go f.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		f.wg.Wait()
	})
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve() {
	defer f.wg.Done()
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handle(conn)
		}()
	}
}

func (f *fakeSMTP) record(verb string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verbs = append(f.verbs, verb)
}

func (f *fakeSMTP) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	isTLS := false
	if f.implicit {
		conn = tls.Server(conn, f.tlsCfg)
		isTLS = true
	}
	r := bufio.NewReader(conn)
	write := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }

	write("220 fake ESMTP")
	var cur capturedMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		f.record(verb)
		switch verb {
		case "EHLO", "HELO":
			write("250-fake")
			if f.starttls && !isTLS {
				write("250-STARTTLS")
			}
			if f.auth {
				write("250-AUTH PLAIN")
			}
			write("250 8BITMIME")
		case "STARTTLS":
			write("220 ready")
			conn = tls.Server(conn, f.tlsCfg)
			r = bufio.NewReader(conn)
			isTLS = true
		case "AUTH":
			f.mu.Lock()
			f.authN++
			f.authOver = append(f.authOver, isTLS)
			f.mu.Unlock()
			write("235 ok")
		case "MAIL":
			cur = capturedMail{from: line}
			write("250 ok")
		case "RCPT":
			cur.rcpts = append(cur.rcpts, line)
			write("250 ok")
		case "DATA":
			write("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, ".")) // undo dot-stuffing
			}
			cur.data = b.String()
			f.mu.Lock()
			f.mails = append(f.mails, cur)
			f.mu.Unlock()
			write("250 queued")
		case "QUIT":
			write("221 bye")
			return
		default:
			write("500 unknown")
		}
	}
}

func (f *fakeSMTP) snapshot() (verbs []string, authN int, authOver []bool, mails []capturedMail) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.verbs...), f.authN, append([]bool(nil), f.authOver...), append([]capturedMail(nil), f.mails...)
}

// selfSignedTLS returns a server config for 127.0.0.1 and the pool that trusts it.
func selfSignedTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake smtp"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, pool
}
