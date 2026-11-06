package subscription_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// Keep the production API and two isolated agents alive for an external browser.
// The owner explicitly releases the private socket after real UI acceptance.
func historyLiveBrowserHandoff(t *testing.T, ctx context.Context, pool *pgxpool.Pool, api *historyLiveProcess, binaries, dir, base, nodeA, group, user, device, token, handoff string) {
	t.Helper()
	nodeB, nodeKey, chain := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	execSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(`UPDATE nodes SET name='History browser A' WHERE id=$1`, nodeA)
	execSQL(`INSERT INTO nodes(id,name,api_key,ip,role,status,publish_direct,tls_cert_file,tls_key_file) VALUES($1,'History browser B',$2,'169.254.220.2','exit','online',true,'/etc/node-agent/certs/cert.pem','/etc/node-agent/certs/key.pem')`, nodeB, nodeKey)
	execSQL(`INSERT INTO inbounds(node_id,protocol,port,tag,settings) VALUES($1,'hysteria2',24443,'hysteria2','{}')`, nodeB)
	execSQL(`INSERT INTO node_group_nodes VALUES($1,$2)`, group, nodeB)
	execSQL(`INSERT INTO node_chains(id,name,exit_node_id,exit_port,transport,health) VALUES($1,'History browser B',$2,24443,'udp','healthy')`, chain, nodeB)
	execSQL(`INSERT INTO node_group_chains(node_group_id,chain_id) VALUES($1,$2)`, group, chain)

	suffix := strings.ReplaceAll(crypto.NewUUID(), "-", "")[:8]
	ns, host, peer := "phb"+suffix, "pbh"+suffix, "pbp"+suffix
	historyLiveCommand(t, "ip", "netns", "add", ns)
	t.Cleanup(func() {
		historyLiveCommand(t, "ip", "netns", "delete", ns)
		t.Logf("CLEANUP browser namespace=%s deleted", ns)
	})
	historyLiveCommand(t, "ip", "link", "add", host, "type", "veth", "peer", "name", peer)
	t.Cleanup(func() { historyLiveCommand(t, "ip", "link", "delete", host) })
	historyLiveCommand(t, "ip", "link", "set", peer, "netns", ns)
	historyLiveCommand(t, "ip", "addr", "add", "169.254.220.1/30", "dev", host)
	historyLiveCommand(t, "ip", "link", "set", host, "up")
	historyLiveCommand(t, "ip", "-n", ns, "addr", "add", "169.254.220.2/30", "dev", peer)
	historyLiveCommand(t, "ip", "-n", ns, "link", "set", peer, "up")
	historyLiveCommand(t, "ip", "-n", ns, "link", "set", "lo", "up")

	nodeDir := filepath.Join(dir, "node-b")
	if err := os.MkdirAll(filepath.Join(nodeDir, "etc/node-agent/certs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "bin"), filepath.Join(nodeDir, "bin")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cert.pem", "key.pem"} {
		data, err := os.ReadFile(filepath.Join(dir, "etc/node-agent/certs", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nodeDir, "etc/node-agent/certs", name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	serverURL := strings.Replace(base, "127.0.0.1", "169.254.220.1", 1)
	historyLiveWrite(t, filepath.Join(nodeDir, "etc/node-agent/config.json"), map[string]any{
		"server_url": serverURL, "node_id": nodeB, "api_key": nodeKey,
	})
	mark := api.mark()
	agentB := historyLiveStart(t, dir, "agent-b", nil, "ip", "netns", "exec", ns, "unshare", "--mount", "--propagation", "private", "sh", "-c",
		`mount --bind "$1/bin" /usr/local/bin && mount --bind "$1/etc" /etc && exec "$2" run --config /etc/node-agent/config.json`, "history-agent-b", nodeDir, filepath.Join(binaries, "node-agent"))
	controlB := "/api/v1/admin/nodes/" + nodeB + "/connection-logging"
	for {
		mark = api.wait(t, ctx, mark, "POST | /api/v1/nodes/"+nodeB+"/heartbeat")
		var status map[string]any
		historyLiveJSON(t, base, http.MethodGet, controlB, token, "", nil, http.StatusOK, &status)
		if status["state"] == "off" {
			producers, ok := status["producers"].([]any)
			if !ok || len(producers) != 1 || status["enabled"] != false {
				t.Fatalf("node B did not publish actual OFF supplier status: %+v", status)
			}
			break
		}
	}

	echoListener, err := net.Listen("tcp", "169.254.219.1:0")
	if err != nil {
		t.Fatal(err)
	}
	echoServer := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		io.WriteString(w, "browser-history-proof\n")
	})}
	echoDone := make(chan error, 1)
	go func() { echoDone <- echoServer.Serve(echoListener) }()
	defer func() { echoServer.Close(); <-echoDone }()

	releasePath := filepath.Join(dir, "browser-release.sock")
	releaseListener, err := net.Listen("unix", releasePath)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	var once sync.Once
	releaseServer := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/release" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		once.Do(func() { close(released) })
	})}
	releaseDone := make(chan error, 1)
	go func() { releaseDone <- releaseServer.Serve(releaseListener) }()
	defer func() { releaseServer.Close(); <-releaseDone }()

	historyLiveWrite(t, handoff, map[string]any{
		"api_guest_url": base, "admin_token": token, "node_a": nodeA, "node_b": nodeB,
		"user_id": user, "device_id": device, "fixture_directory": dir,
		"socks_proxy":    "169.254.219.2:20801",
		"traffic_url":    "http://" + echoListener.Addr().String() + "/browser-proof",
		"release_socket": releasePath, "agent_b_pid": agentB.cmd.Process.Pid,
	})
	t.Logf("LIVE_BROWSER_READY handoff=%s node_a=%s node_b=%s api=%s", handoff, nodeA, nodeB, base)
	select {
	case <-released:
		t.Log("LIVE_BROWSER_RELEASED")
	case <-ctx.Done():
		t.Fatal(fmt.Errorf("browser acceptance did not release fixture: %w", ctx.Err()))
	}
}
