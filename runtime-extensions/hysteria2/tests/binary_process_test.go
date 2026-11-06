package server

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/client"
	"github.com/stretchr/testify/require"
)

type historyProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	addr   net.Addr
	socket string
}

func historyCertificate(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0600))
}

func historyStartBinary(t *testing.T, evidence, udpIdleTimeout string) *historyProcess {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "proxima-h-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	historyCertificate(t, dir)
	reservation, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := reservation.LocalAddr()
	require.NoError(t, reservation.Close())
	config := map[string]any{
		"listen":         addr.String(),
		"tls":            map[string]string{"cert": filepath.Join(dir, "cert.pem"), "key": filepath.Join(dir, "key.pem")},
		"auth":           map[string]any{"type": "userpass", "userpass": map[string]string{"alice": "probe-a", "bob": "probe-b"}},
		"udpIdleTimeout": udpIdleTimeout,
		"sniff":          map[string]any{"enable": true, "rewriteDomain": true},
	}
	b, err := json.Marshal(config)
	require.NoError(t, err)
	configPath := filepath.Join(dir, "server.json")
	require.NoError(t, os.WriteFile(configPath, b, 0600))
	binary := os.Getenv("HYSTERIA_BINARY")
	require.NotEmpty(t, binary)
	cmd := exec.Command(binary, "server", "--config", configPath, "--disable-update-check", "--log-format", "json")
	socket := filepath.Join(dir, "history.sock")
	cmd.Env = append(os.Environ(), "PROXIMA_HISTORY_SOCKET="+socket)
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)
	logfile, err := os.Create(filepath.Join(evidence, "runtime.log"))
	require.NoError(t, err)
	ready, logsDone := make(chan struct{}, 1), make(chan struct{})
	// Subscribe to the exact startup log before starting the process.
	go func() {
		defer close(logsDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			logfile.WriteString(line + "\n")
			var record struct {
				Message string `json:"msg"`
			}
			if json.Unmarshal([]byte(line), &record) == nil && record.Message == "server up and running" {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	}()
	require.NoError(t, cmd.Start())
	p := &historyProcess{cmd: cmd, done: make(chan struct{}), addr: addr, socket: socket}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-p.done:
			case <-time.After(10 * time.Second):
				cmd.Process.Kill()
				<-p.done
				t.Error("runtime failed graceful shutdown")
			}
		}
		<-logsDone
		require.NoError(t, logfile.Close())
	})
	select {
	case <-ready:
	case <-p.done:
		t.Fatalf("runtime exited before ready: %v", p.err)
	case <-time.After(15 * time.Second):
		t.Fatal("runtime readiness deadline")
	}
	return p
}

func (p *historyProcess) client(t *testing.T, user string) client.Client {
	t.Helper()
	password := "probe-a"
	if strings.HasPrefix(user, "bob") {
		password = "probe-b"
	}
	c, _, err := client.NewClient(&client.Config{
		ServerAddr: p.addr, Auth: user + ":" + password,
		TLSConfig: client.TLSConfig{ServerName: "localhost", InsecureSkipVerify: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}
