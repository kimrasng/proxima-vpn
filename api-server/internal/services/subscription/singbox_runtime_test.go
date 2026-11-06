package subscription_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

// This opt-in Linux proof uses production API/agent executables, actual patched
// runtimes, a private network/mount namespace, its own database and Redis.
func TestSingboxHysteriaRealAgentLifecycle(t *testing.T) {
	binaries := os.Getenv("HISTORY_LIVE_BINARIES")
	if binaries == "" {
		t.Skip("HISTORY_LIVE_BINARIES enables real Linux lifecycle proof")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("live proof requires owned Linux root namespace privileges")
	}
	timeout := 4 * time.Minute
	if os.Getenv("HISTORY_LIVE_UI_HANDOFF") != "" {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	dir, err := os.MkdirTemp("/tmp", "phi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { t.Logf("LIVE_EVIDENCE=%s", dir) })
	admin, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	dbName := "history_i_live_" + strings.ReplaceAll(crypto.NewUUID(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = dbName
	databaseURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	databaseURL.Path = "/" + dbName
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
			t.Error(err)
		}
		admin.Close(context.Background())
		t.Logf("CLEANUP database=%s dropped", dbName)
	})
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(crypto.NewUUID(), "-", "")[:8]
	ns, host, peer := "phi"+suffix, "phh"+suffix, "php"+suffix
	historyLiveCommand(t, "ip", "netns", "add", ns)
	t.Cleanup(func() { historyLiveCommand(t, "ip", "netns", "delete", ns); t.Logf("CLEANUP namespace=%s deleted", ns) })
	historyLiveCommand(t, "ip", "link", "add", host, "type", "veth", "peer", "name", peer)
	t.Cleanup(func() { historyLiveCommand(t, "ip", "link", "delete", host) })
	historyLiveCommand(t, "ip", "link", "set", peer, "netns", ns)
	historyLiveCommand(t, "ip", "addr", "add", "169.254.219.1/30", "dev", host)
	historyLiveCommand(t, "ip", "link", "set", host, "up")
	historyLiveCommand(t, "ip", "-n", ns, "addr", "add", "169.254.219.2/30", "dev", peer)
	historyLiveCommand(t, "ip", "-n", ns, "link", "set", peer, "up")
	historyLiveCommand(t, "ip", "-n", ns, "link", "set", "lo", "up")
	for _, path := range []string{"bin", "etc/node-agent/certs"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"xray", "hysteria"} {
		data, err := os.ReadFile(filepath.Join(binaries, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bin", name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hysteriaBinary, err := os.ReadFile(filepath.Join(binaries, "hysteria"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(hysteriaBinary)
	artifact := map[string]any{"runtime": "hysteria2", "commit": "ca8fbd874fd6ca82413948de6dc5924405e103e1", "version": "v2.13.0", "build": "hysteria2-ca8fbd874fd6-history-v1", "os": "linux", "arch": runtime.GOARCH, "path": filepath.Join(binaries, "hysteria"), "sha256": strings.Repeat("0", 64)}
	manifest := map[string]any{"schema_version": 1, "artifacts": []map[string]any{artifact}}
	manifestPath := filepath.Join(dir, "runtime-manifest.json")
	historyLiveWrite(t, manifestPath, manifest)
	installArgs := []string{"install-runtime", "--runtime", "hysteria2", "--manifest", manifestPath, "--destination", filepath.Join(dir, "bin/hysteria")}
	if err := exec.Command(filepath.Join(binaries, "node-agent"), installArgs...).Run(); err == nil {
		t.Fatal("runtime installer accepted a corrupt checksum")
	}
	artifact["sha256"] = hex.EncodeToString(sum[:])
	historyLiveWrite(t, manifestPath, manifest)
	historyLiveCommand(t, append([]string{filepath.Join(binaries, "node-agent")}, installArgs...)...)
	t.Logf("RUNTIME_STAGE_PASS runtime=hysteria2 sha256=%s", artifact["sha256"])
	certDir := filepath.Join(dir, "etc/node-agent/certs")
	historyLiveCommand(t, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", certDir+"/key.pem", "-out", certDir+"/cert.pem", "-subj", "/CN=history.test", "-days", "1")
	node, user, device, group, plan, chain, adminID := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	credential, subToken, nodeKey := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	execSQL := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(`INSERT INTO admins(id,email,password_hash) VALUES($1,'live@example.test','unused')`, adminID)
	execSQL(`INSERT INTO nodes(id,name,api_key,ip,role,status,publish_direct,tls_cert_file,tls_key_file) VALUES($1,'History live',$2,'169.254.219.2','exit','online',true,'/etc/node-agent/certs/cert.pem','/etc/node-agent/certs/key.pem')`, node, nodeKey)
	execSQL(`INSERT INTO inbounds(node_id,protocol,port,tag,settings) VALUES($1,'hysteria2',24443,'hysteria2','{}')`, node)
	execSQL(`INSERT INTO node_groups(id,name) VALUES($1,'History live')`, group)
	execSQL(`INSERT INTO node_group_nodes VALUES($1,$2)`, group, node)
	execSQL(`INSERT INTO plans(id,name,duration_days,node_group_id) VALUES($1,'History live',30,$2)`, plan, group)
	execSQL(`INSERT INTO users(id,email,password_hash,sub_token,plan_id,is_active,status,traffic_used) VALUES($1,'live-user@example.test','unused',$2,$3,true,'active',713)`, user, subToken, plan)
	execSQL(`INSERT INTO devices(id,user_id,name,xray_uuid) VALUES($1,$2,'Live client',$3)`, device, user, credential)
	execSQL(`INSERT INTO node_chains(id,name,exit_node_id,exit_port,transport,health) VALUES($1,'History live',$2,24443,'udp','healthy')`, chain, node)
	execSQL(`INSERT INTO node_group_chains(node_group_id,chain_id) VALUES($1,$2)`, group, chain)
	redisPort, apiPort := historyLivePort(t), historyLivePort(t)
	redis := historyLiveStart(t, dir, "redis", nil, "redis-server", "--bind", "127.0.0.1", "--port", fmt.Sprint(redisPort), "--save", "", "--appendonly", "no", "--dir", dir)
	redis.wait(t, ctx, 0, "Ready to accept connections")
	const secret = "history-i-local-proof-secret"
	apiConfig := filepath.Join(dir, "api.json")
	historyLiveWrite(t, apiConfig, map[string]any{"server": map[string]any{"host": "0.0.0.0", "port": apiPort}, "storage": map[string]any{"local_path": dir + "/uploads"}})
	api := historyLiveStart(t, dir, "api", []string{"CONFIG_PATH=" + apiConfig, "DATABASE_URL=" + databaseURL.String(), "REDIS_URL=" + fmt.Sprintf("redis://127.0.0.1:%d/0", redisPort), "JWT_SECRET=" + secret, "SWAGGER_ENABLED=false"}, filepath.Join(binaries, "api-server"))
	api.wait(t, ctx, 0, "http://")
	base := fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	claims := handlers.AdminClaims{AdminID: adminID, Email: "live@example.test", RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	nodePath := "/api/v1/nodes/" + node
	controlPath := "/api/v1/admin/nodes/" + node + "/connection-logging"
	var historyConfig services.ConnectionHistoryConfig
	historyLiveJSON(t, base, "GET", nodePath+"/connection-history/config", "", "wrong", nil, 401, nil)
	historyLiveJSON(t, base, "GET", nodePath+"/connection-history/config", "", nodeKey, nil, 200, &historyConfig)
	if len(historyConfig.Bindings) != 1 || historyConfig.Bindings[0].Label != credential {
		t.Fatal("actual Hysteria authenticated label not bound")
	}
	oldRef := historyConfig.Bindings[0].CredentialRef
	// New nodes permit one inbound each. Exercise actual generated labels on
	// separate nodes rather than bypass that production schema constraint.
	for _, protocol := range []string{"vmess_ws", "trojan_tls", "shadowsocks"} {
		bindingNode := crypto.NewUUID()
		execSQL(`INSERT INTO nodes(id,name,api_key,ip,role,status,tls_cert_file,tls_key_file) VALUES($1,'History binding',$2,'169.254.219.2','exit','online','/etc/node-agent/certs/cert.pem','/etc/node-agent/certs/key.pem')`, bindingNode, nodeKey)
		execSQL(`INSERT INTO node_group_nodes VALUES($1,$2)`, group, bindingNode)
		execSQL(`INSERT INTO inbounds(node_id,protocol,port,tag,settings) VALUES($1,$2,24444,$2,'{"path":"/live","method":"aes-256-gcm","password":"local-fixture-shared"}')`, bindingNode, protocol)
		bindingPath := "/api/v1/nodes/" + bindingNode
		var generatedConfig struct {
			Inbounds []struct {
				Tag      string `json:"tag"`
				Settings struct {
					Clients []struct {
						Email string `json:"email"`
					} `json:"clients"`
				} `json:"settings"`
			} `json:"inbounds"`
		}
		var generatedHistory services.ConnectionHistoryConfig
		historyLiveJSON(t, base, "GET", bindingPath+"/config", "", nodeKey, nil, 200, &generatedConfig)
		historyLiveJSON(t, base, "GET", bindingPath+"/connection-history/config", "", nodeKey, nil, 200, &generatedHistory)
		if len(generatedHistory.Bindings) != 1 {
			t.Fatalf("%s generated bindings=%d, want one", protocol, len(generatedHistory.Bindings))
		}
		binding := generatedHistory.Bindings[0]
		if protocol == "shadowsocks" {
			if binding.Kind != "shared" || binding.Label != protocol {
				t.Fatalf("shared credential gained a device owner: %+v", binding)
			}
		} else {
			found := false
			for _, inbound := range generatedConfig.Inbounds {
				for _, configured := range inbound.Settings.Clients {
					if binding.InboundTag != inbound.Tag || binding.Label != configured.Email || binding.Kind != "device" {
						t.Fatal("binding did not use the complete actual authenticated email")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("generated config omitted eligible device")
			}
		}
		execSQL(`DELETE FROM nodes WHERE id=$1`, bindingNode)
	}
	historyLiveJSON(t, base, "GET", nodePath+"/connection-history/config", "", nodeKey, nil, 200, &historyConfig)
	if len(historyConfig.Bindings) != 1 || historyConfig.Bindings[0].CredentialRef != oldRef {
		t.Fatal("unrelated runtime removal rotated retained Hysteria credential")
	}
	historyLiveWrite(t, dir+"/etc/node-agent/config.json", map[string]any{"server_url": fmt.Sprintf("http://169.254.219.1:%d", apiPort), "node_id": node, "api_key": nodeKey})
	mark := api.mark()
	agent := historyLiveStart(t, dir, "agent", nil, "ip", "netns", "exec", ns, "unshare", "--mount", "--propagation", "private", "sh", "-c",
		`mount --bind "$1/bin" /usr/local/bin && mount --bind "$1/etc" /etc && exec "$2" run --config /etc/node-agent/config.json`, "history-agent", dir, filepath.Join(binaries, "node-agent"))
	readControl := func() map[string]any {
		t.Helper()
		var out map[string]any
		historyLiveJSON(t, base, "GET", controlPath, token, "", nil, 200, &out)
		return out
	}
	waitState := func(after int, state string) map[string]any {
		t.Helper()
		for {
			after = api.wait(t, ctx, after, "POST | "+nodePath+"/heartbeat")
			out := readControl()
			t.Logf("STATUS observed=%v wanted=%s desired=%v applied=%v error=%v queue=%v",
				out["state"], state, out["desired_revision"], out["applied_revision"], out["error_code"], out["queue_bytes"])
			if out["state"] == state {
				t.Logf("STATUS %s revision=%v epoch=%v producers=%v", state, out["applied_revision"], out["applied_collection_epoch"], out["producers"])
				return out
			}
		}
	}
	initial := waitState(mark, "off")
	var initialDigest, finalDigest services.ConfigDigest
	historyLiveJSON(t, base, "GET", nodePath+"/config/digest", "", nodeKey, nil, 200, &initialDigest)
	var profile struct {
		Outbounds []struct {
			Type     string `json:"type"`
			Server   string `json:"server"`
			Port     int    `json:"server_port"`
			Password string `json:"password"`
		} `json:"outbounds"`
	}
	historyLiveJSON(t, base, "GET", "/sub/"+subToken+"/"+device+"?format=singbox", "", "", nil, 200, &profile)
	auth := ""
	for _, out := range profile.Outbounds {
		if out.Type == "hysteria2" {
			auth = out.Password
		}
	}
	if auth != credential+":"+credential {
		t.Fatal("public profile omitted or mismatched userpass")
	}
	historyLiveWrite(t, dir+"/client.json", map[string]any{"server": "127.0.0.1:24443", "auth": auth, "tls": map[string]any{"insecure": true}, "socks5": map[string]any{"listen": "0.0.0.0:20801"}})
	hyClient := historyLiveStart(t, dir, "client", nil, "ip", "netns", "exec", ns, filepath.Join(binaries, "hysteria"), "client", "--config", dir+"/client.json")
	hyClient.wait(t, ctx, 0, "SOCKS5 server listening")
	echo, err := net.Listen("tcp", "169.254.219.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		conn, err := echo.Accept()
		if err == nil {
			defer conn.Close()
			io.Copy(conn, conn)
		}
	}()
	conn := historyLiveSOCKS(t, "169.254.219.2:20801", echo.Addr().String())
	defer conn.Close()
	payload := func(label string) {
		t.Helper()
		data := []byte(label)
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(data); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(data))
		if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("payload %s: %v", label, err)
		}
		conn.SetDeadline(time.Time{})
	}
	payload("before-enable")
	toggle := func(enabled bool, state string) map[string]any {
		t.Helper()
		current := readControl()
		before := api.mark()
		historyLiveJSON(t, base, "POST", "/api/v1/admin/nodes/connection-logging", token, "", map[string]any{"nodes": []map[string]any{{"id": node, "expected_revision": current["desired_revision"]}}, "enabled": enabled}, 200, nil)
		return waitState(before, state)
	}
	on := toggle(true, "collecting")
	payload("first-epoch")
	toggle(false, "off")
	payload("while-disabled")
	second := toggle(true, "collecting")
	if on["collection_epoch"] == second["collection_epoch"] {
		t.Fatal("reenable reused epoch")
	}
	producerBoot := func(status map[string]any) any {
		t.Helper()
		producers, ok := status["producers"].([]any)
		if !ok || len(producers) != 1 {
			t.Fatal("expected the actual Hysteria supplier")
		}
		producer, ok := producers[0].(map[string]any)
		if !ok {
			t.Fatal("invalid producer status")
		}
		return producer["observer_boot_id"]
	}
	if producerBoot(initial) == nil || producerBoot(initial) != producerBoot(second) {
		t.Fatal("toggle restarted runtime observer")
	}
	historyLiveJSON(t, base, "GET", nodePath+"/config/digest", "", nodeKey, nil, 200, &finalDigest)
	if initialDigest.Hash != finalDigest.Hash {
		t.Fatal("logging toggle changed VPN config digest")
	}
	payload("second-epoch")
	beforeEnd := api.mark()
	conn.Close()
	<-echoDone
	for {
		beforeEnd = api.wait(t, ctx, beforeEnd, "POST | "+nodePath+"/connection-history")
		var page services.ConnectionHistoryPage
		historyLiveJSON(t, base, "GET", "/api/v1/admin/connection-history/flows?node_id="+node, token, "", nil, 200, &page)
		final := false
		for _, row := range page.Items {
			if row.Event.Kind == "flow_end" && row.Event.Bytes.Final {
				final = true
				if row.Event.Auth.Kind != "device" || row.Event.Auth.CredentialRef == nil || *row.Event.Auth.CredentialRef != oldRef ||
					row.Event.Bytes.Upload == nil || row.Event.Bytes.Download == nil ||
					uint64(*row.Event.Bytes.Upload) != uint64(len("second-epoch")) ||
					uint64(*row.Event.Bytes.Download) != uint64(len("second-epoch")) ||
					!row.Event.Lifecycle.PartialStart || string(row.Event.CollectionEpoch) != second["collection_epoch"] {
					t.Fatalf("held flow attribution/epoch/counters mismatch: %+v", row.Event)
				}
				t.Logf("FLOW_FINAL event=%s auth=%s upload=%s download=%s partial_start=%t", row.Event.EventID, row.Event.Auth.Kind, row.Event.Bytes.Upload.String(), row.Event.Bytes.Download.String(), row.Event.Lifecycle.PartialStart)
			}
		}
		if final {
			break
		}
	}
	execSQL(`UPDATE inbounds SET enabled=false WHERE node_id=$1`, node)
	historyLiveJSON(t, base, "GET", "/sub/"+subToken+"/"+device+"?format=singbox", "", "", nil, 503, nil)
	execSQL(`UPDATE inbounds SET enabled=true WHERE node_id=$1`, node)
	execSQL(`UPDATE users SET is_active=false WHERE id=$1`, user)
	historyLiveJSON(t, base, "GET", "/sub/"+subToken+"/"+device+"?format=singbox", "", "", nil, 403, nil)
	execSQL(`UPDATE users SET is_active=true WHERE id=$1`, user)
	// Rotate and revoke through the production private config path.
	execSQL(`UPDATE devices SET xray_uuid=$2 WHERE id=$1`, device, crypto.NewUUID())
	historyLiveJSON(t, base, "GET", nodePath+"/connection-history/config", "", nodeKey, nil, 200, &historyConfig)
	if len(historyConfig.Bindings) != 1 || historyConfig.Bindings[0].CredentialRef == oldRef {
		t.Fatal("rotation reused immutable binding")
	}
	execSQL(`DELETE FROM devices WHERE id=$1`, device)
	historyLiveJSON(t, base, "GET", nodePath+"/connection-history/config", "", nodeKey, nil, 200, &historyConfig)
	if len(historyConfig.Bindings) != 0 {
		t.Fatal("deleted device retained active binding")
	}
	var used int64
	if err := pool.QueryRow(ctx, `SELECT traffic_used FROM users WHERE id=$1`, user).Scan(&used); err != nil || used != 713 {
		t.Fatalf("billing changed: %d %v", used, err)
	}
	var rev nodeprov.HistoryU64
	rev = historyConfig.Revision
	t.Logf("LIVE_CONNECTED_PASS agent_pid=%d final_binding_revision=%s quota=%d", agent.cmd.Process.Pid, rev.String(), used)
	if handoff := os.Getenv("HISTORY_LIVE_UI_HANDOFF"); handoff != "" {
		execSQL(`INSERT INTO devices(id,user_id,name,xray_uuid) VALUES($1,$2,'Live client',$3)`, device, user, credential)
		historyLiveJSON(t, base, "GET", nodePath+"/connection-history/config", "", nodeKey, nil, 200, &historyConfig)
		toggle(false, "off")
		historyLiveBrowserHandoff(t, ctx, pool, api, binaries, dir, base, node, group, user, device, token, handoff)
	}
}
