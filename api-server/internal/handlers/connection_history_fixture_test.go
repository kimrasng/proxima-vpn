package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/handlers"
	"github.com/proximavpn/proxima-vpn/api-server/internal/middleware"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

type historyHTTPFixture struct {
	t                         *testing.T
	db                        *pgxpool.Pool
	app                       *fiber.App
	svc                       *services.ConnectionHistoryService
	node, other, user, device string
	token                     string
	binding                   nodeprov.HistoryBinding
	config                    services.ConnectionHistoryConfig
	boot                      nodeprov.HistoryUUID
	now                       time.Time
}

func historyUUID(t *testing.T) nodeprov.HistoryUUID {
	t.Helper()
	v, err := nodeprov.NewHistoryUUID()
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func historyFixture(t *testing.T) *historyHTTPFixture {
	return historyFixtureWithProtocol(t, "vless_reality")
}
func historyFixtureWithProtocol(t *testing.T, protocol string) *historyHTTPFixture {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required; integration tests never skip")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "history_s_" + strings.ReplaceAll(string(historyUUID(t)), "-", "")
	if _, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, `DROP DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Error(err)
		}
		t.Logf("CLEANUP database=%s dropped", name)
	})
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = database.MigrateConnectionHistory(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	f := &historyHTTPFixture{t: t, db: pool, svc: services.NewConnectionHistoryService(pool), boot: historyUUID(t), now: time.Now().UTC()}
	for i, target := range []*string{&f.node, &f.other} {
		err = pool.QueryRow(ctx, `INSERT INTO nodes(name,api_key,ip,role,status,last_seen) VALUES($1,$2,$3,'exit','online',now()) RETURNING id::text`, fmt.Sprintf("history-node-%d", i), fmt.Sprintf("history-key-%d", i), fmt.Sprintf("192.0.2.%d", i+10)).Scan(target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO inbounds(node_id,protocol,port,tag) VALUES($1,$2,$3,'vless-test')`, *target, protocol, 4400+i); err != nil {
			t.Fatal(err)
		}
	}
	err = pool.QueryRow(ctx, `INSERT INTO users(email,password_hash,sub_token,traffic_used) VALUES('history@example.test','unused','unused-token',713) RETURNING id::text`).Scan(&f.user)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO devices(user_id,name,xray_uuid) VALUES($1,'history-phone',$2) RETURNING id::text`, f.user, string(historyUUID(t))).Scan(&f.device)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO traffic_logs(device_id,node_id,up_bytes,dn_bytes) VALUES($1,$2,91,137)`, f.device, f.node); err != nil {
		t.Fatal(err)
	}
	var adminID string
	if err = pool.QueryRow(ctx, `INSERT INTO admins(email,password_hash) VALUES('history-admin@example.test','unused') RETURNING id::text`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	claims := handlers.AdminClaims{AdminID: adminID, Email: "history-admin@example.test", RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	f.token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("history-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	f.app = fiber.New(fiber.Config{BodyLimit: 2 << 20})
	h := handlers.NewConnectionHistoryHandler(f.svc)
	node := f.app.Group("/api/v1/nodes/:id", middleware.NodeAPIKeyMiddleware(pool))
	adm := f.app.Group("/api/v1/admin", middleware.AdminJWTMiddleware("history-test-secret", pool))
	h.RegisterRoutes(node, adm)
	t.Cleanup(func() {
		if err := f.app.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *historyHTTPFixture) request(method, path string, payload any, key string, want int) []byte {
	f.t.Helper()
	var body []byte
	var err error
	switch v := payload.(type) {
	case nil:
	case []byte:
		body = v
	default:
		body, err = json.Marshal(v)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	// Record the exact HTTP action and payload BEFORE execution.
	f.t.Logf("HTTP REQUEST %s %s X-Node-Key=%q admin=%t body=%s", method, path, key, strings.HasPrefix(path, "/api/v1/admin"), body)
	before := f.dbState()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Node-Key", key)
	}
	if strings.HasPrefix(path, "/api/v1/admin") {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	resp, err := f.app.Test(req, 15000)
	if err != nil {
		f.t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = resp.Body.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.t.Logf("HTTP RESPONSE status=%d headers=%v body=%s DB before=%s after=%s", resp.StatusCode, resp.Header, data, before, f.dbState())
	if resp.StatusCode != want {
		f.t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, want, data)
	}
	return data
}
func (f *historyHTTPFixture) dbState() string {
	f.t.Helper()
	var state string
	err := f.db.QueryRow(context.Background(), `SELECT json_build_object(
 'controls',(SELECT coalesce(json_agg(json_build_object('node',node_origin,'revision',revision::text,'enabled',enabled)),'[]') FROM connection_log_controls),
 'events',(SELECT count(*) FROM connection_log_events),'receipts',(SELECT count(*) FROM connection_log_event_receipts),
 'batches',(SELECT count(*) FROM connection_log_batches),'flows',(SELECT count(*) FROM connection_flows),'gaps',(SELECT count(*) FROM connection_log_gaps),
 'quota',(SELECT sum(traffic_used) FROM users),'traffic',(SELECT sum(up_bytes+dn_bytes) FROM traffic_logs))::text`).Scan(&state)
	if err != nil {
		f.t.Fatal(err)
	}
	return state
}
func (f *historyHTTPFixture) status(node string) {
	f.t.Helper()
	status := services.ConnectionHistoryStatus{State: "off", Producers: []services.ConnectionHistoryProducerStatus{{
		ProducerID: "xray", ObserverBootID: &f.boot, RuntimeBuild: "real-fixture-history-v1",
		Capabilities: []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"},
	}}}
	if err := f.svc.RecordStatus(context.Background(), node, status); err != nil {
		f.t.Fatal(err)
	}
}
func (f *historyHTTPFixture) enable() {
	f.t.Helper()
	name, device := "history@example.test", "history-phone"
	var err error
	f.binding, err = f.svc.IssueBinding(context.Background(), f.node, 0, nodeprov.HistoryBinding{Runtime: "xray", InboundTag: "vless-test", LabelKind: "xray_email", Label: "private-configured-label", Kind: "device"}, services.ConnectionHistoryBindingOwner{UserID: &f.user, DeviceID: &f.device, UserSnapshot: &name, DeviceSnapshot: &device})
	if err != nil {
		f.t.Fatal(err)
	}
	f.status(f.node)
	f.request("POST", "/api/v1/admin/nodes/connection-logging", services.ConnectionHistoryBulkCommand{Nodes: []services.ConnectionHistoryNodeCAS{{ID: nodeprov.HistoryUUID(f.node), ExpectedRevision: 1}}, Enabled: true}, "", 200)
	data := f.request("GET", "/api/v1/nodes/"+f.node+"/connection-history/config", nil, "history-key-0", 200)
	if err = json.Unmarshal(data, &f.config); err != nil {
		f.t.Fatal(err)
	}
	if !f.config.Enabled || f.config.Revision != 2 || f.config.CollectionEpoch == nil {
		f.t.Fatalf("config=%s", data)
	}
}
func historyPtr[T any](v T) *T { return &v }
func (f *historyHTTPFixture) event(sequence uint64, kind string, flow int, upload, download uint64) nodeprov.HistoryEvent {
	ts := nodeprov.HistoryTimestamp(f.now)
	e := nodeprov.HistoryEvent{SchemaVersion: 1, EventID: string(f.boot) + fmt.Sprintf(":%d", sequence), ObserverBootID: f.boot, Sequence: nodeprov.HistoryU64(sequence),
		ProducerID: "xray", Runtime: "xray", RuntimeBuild: "fixture-history-v1", ConfigRevision: f.config.Revision, CollectionEpoch: *f.config.CollectionEpoch,
		EventTime: ts, MonotonicNS: nodeprov.HistoryU64(sequence * 100), Kind: kind,
		TunnelID: historyPtr(string(f.boot) + ":t:1"), FlowID: historyPtr(fmt.Sprintf("%s:f:%d", f.boot, flow)), Protocol: historyPtr("vless"), InboundTag: historyPtr("vless-test"),
		Auth:        nodeprov.HistoryAuth{Kind: "device", CredentialRef: &f.binding.CredentialRef, BindingRevision: &f.binding.BindingRevision},
		Peer:        &nodeprov.HistoryPeer{IP: "198.51.100.3", Port: 53127},
		Destination: &nodeprov.HistoryDestination{RequestedHost: historyPtr(fmt.Sprintf("Target-%d.example.test", flow)), RequestedPort: historyPtr(443 + flow), Transport: "tcp"},
		Bytes:       nodeprov.HistoryBytes{Layer: "proxy_payload_observed", Upload: historyPtr(nodeprov.HistoryU64(upload)), Download: historyPtr(nodeprov.HistoryU64(download))},
		Lifecycle:   nodeprov.HistoryLifecycle{Scope: "proxy_flow", StartedAt: &ts, LastObservedAt: &ts},
		Quality:     nodeprov.HistoryQuality{Identity: "exact", Parent: "exact", Destination: "exact", Bytes: "exact", Start: "exact", End: "unknown", Completeness: "complete"},
	}
	if kind == "flow_end" {
		e.Bytes.Final = true
		e.Lifecycle.EndedAt = &ts
		e.Lifecycle.EndReason = historyPtr("eof")
		e.Lifecycle.EndSemantics = historyPtr("local_owner_closed")
		e.Quality.End = "exact"
	}
	return e
}
func (f *historyHTTPFixture) batch(events ...nodeprov.HistoryEvent) services.ConnectionHistoryBatch {
	f.t.Helper()
	digest, err := nodeprov.HistoryEventsDigest(events)
	if err != nil {
		f.t.Fatal(err)
	}
	return services.ConnectionHistoryBatch{SchemaVersion: 1, BatchID: historyUUID(f.t), PayloadSHA256: digest, Events: events}
}
func (f *historyHTTPFixture) ingest(batch services.ConnectionHistoryBatch, want int) []byte {
	return f.request("POST", "/api/v1/nodes/"+f.node+"/connection-history", batch, "history-key-0", want)
}
func (f *historyHTTPFixture) page(query string) services.ConnectionHistoryPage {
	f.t.Helper()
	data := f.request("GET", "/api/v1/admin/connection-history/flows"+query, nil, "", 200)
	var page services.ConnectionHistoryPage
	if err := json.Unmarshal(data, &page); err != nil {
		f.t.Fatal(err)
	}
	return page
}
