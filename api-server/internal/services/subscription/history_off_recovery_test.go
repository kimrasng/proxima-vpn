package subscription_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func TestHistoryOFFRecoveryHTTP(t *testing.T) {
	agent := os.Getenv("HISTORY_OFF_RECOVERY_AGENT")
	if agent == "" {
		t.Skip("HISTORY_OFF_RECOVERY_AGENT must name the connectionlog test executable")
	}
	for _, restart := range []string{"0", "1"} {
		t.Run("producer_restart_"+restart, func(t *testing.T) {
			// Given production authenticated HTTP routes and a dedicated DB.
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			check := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			dsn := os.Getenv("TEST_DATABASE_URL")
			admin, err := pgx.Connect(ctx, dsn)
			check(err)
			dbName := "history_b1_" + strings.ReplaceAll(crypto.NewUUID(), "-", "")
			_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize())
			check(err)
			var pool *pgxpool.Pool
			t.Cleanup(func() {
				if pool != nil {
					pool.Close()
				}
				cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
					t.Error(err)
				}
				if err := admin.Close(cleanupCtx); err != nil {
					t.Error(err)
				}
				t.Logf("CLEANUP database=%s dropped", dbName)
			})
			cfg, err := pgxpool.ParseConfig(dsn)
			check(err)
			cfg.ConnConfig.Database = dbName
			pool, err = pgxpool.NewWithConfig(ctx, cfg)
			check(err)
			check(database.Migrate(ctx, pool))
			var node, adminID string
			check(pool.QueryRow(ctx, `INSERT INTO nodes(name,api_key,ip,role,status,last_seen)
 VALUES('B1 recovery','b1-key','192.0.2.45','exit','online',now()) RETURNING id::text`).Scan(&node))
			_, err = pool.Exec(ctx, `INSERT INTO inbounds(node_id,protocol,port,tag) VALUES($1,'vless_reality',14443,'b1')`, node)
			check(err)
			check(pool.QueryRow(ctx, `INSERT INTO admins(email,password_hash) VALUES('b1@example.test','unused') RETURNING id::text`).Scan(&adminID))
			service := services.NewConnectionHistoryService(pool)
			boot := nodeprov.HistoryUUID(crypto.NewUUID())
			// Capabilities seed only the normal bulk-control precondition.
			// Event and revision admission always run through production code.
			check(service.RecordStatus(ctx, node, services.ConnectionHistoryStatus{
				State: "off", Producers: []services.ConnectionHistoryProducerStatus{{
					ProducerID: "xray", ObserverBootID: &boot, RuntimeBuild: "b1-shared-exporter",
					Capabilities: []string{"auth_transport", "hot_toggle", "mux", "tcp_flow", "udp_destination", "xudp"},
				}},
			}))
			const secret = "b1-local-jwt-secret"
			claims := handlers.AdminClaims{AdminID: adminID, Email: "b1@example.test",
				RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
			check(err)
			app := fiber.New(fiber.Config{DisableStartupMessage: true, BodyLimit: 2 << 20})
			handler := handlers.NewConnectionHistoryHandler(service, services.NewActivityService(pool))
			handler.RegisterRoutes(
				app.Group("/api/v1/nodes/:id", middleware.NodeAPIKeyMiddleware(pool)),
				app.Group("/api/v1/admin", middleware.AdminJWTMiddleware(secret, pool)),
			)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			check(err)
			ready, stopped := make(chan struct{}), make(chan error, 1)
			app.Hooks().OnListen(func(fiber.ListenData) error { close(ready); return nil })
			go func() { stopped <- app.Listener(listener) }()
			t.Cleanup(func() {
				if err := app.ShutdownWithTimeout(5 * time.Second); err != nil {
					t.Error(err)
				}
				if err := <-stopped; err != nil {
					t.Error(err)
				}
				t.Logf("CLEANUP HTTP listener=%s closed", listener.Addr())
			})
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			base := "http://" + listener.Addr().String()
			// When a real collector is SIGKILLed after applied OFF, optionally
			// replacing its exporter too, then drains and reenables.
			command := exec.CommandContext(ctx, agent, "-test.v", "-test.run=^TestHistoryOFFRecoveryRealHTTP$")
			command.Env = append(os.Environ(), "HISTORY_B1_SERVER="+base, "HISTORY_B1_NODE="+node,
				"HISTORY_B1_ADMIN="+token, "HISTORY_B1_RESTART_PRODUCER="+restart)
			output, err := command.CombinedOutput()
			t.Logf("B1_AGENT_TRANSCRIPT\n%s", output)
			if err != nil {
				t.Fatalf("real collector recovery: %v", err)
			}
			// Then the real service accepts the exact agent OFF policy reports,
			// including the fresh producer's null local epoch / closed policy epoch.
			offReports, onReports := 0, 0
			for _, line := range strings.Split(string(output), "\n") {
				prefix := ""
				switch {
				case strings.HasPrefix(line, "B1_OFF_STATUS "):
					prefix = "B1_OFF_STATUS "
					offReports++
				case strings.HasPrefix(line, "B1_ON_STATUS "):
					prefix = "B1_ON_STATUS "
					onReports++
				default:
					continue
				}
				status, err := services.DecodeConnectionHistoryStatus([]byte(strings.TrimPrefix(line, prefix)))
				check(err)
				check(service.RecordStatus(ctx, node, status))
			}
			if offReports != 2 || onReports != 1 {
				t.Fatalf("missing applied reports: off=%d on=%d", offReports, onReports)
			}
			var gaps, epochs, events int
			check(pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM connection_log_gaps),
 (SELECT count(*) FROM connection_log_epochs),(SELECT count(*) FROM connection_log_events)`).Scan(&gaps, &epochs, &events))
			if gaps != 0 || epochs != 2 || events < 7 {
				t.Fatalf("false OFF loss or undrained history: gaps=%d epochs=%d events=%d", gaps, epochs, events)
			}
			t.Logf("B1_DB gaps=%d epochs=%d events=%d", gaps, epochs, events)
			// Keep a negative control: a gap under the OFF revision must still
			// fail with revision_disabled. No fixture can bypass this guard.
			var raw []byte
			check(pool.QueryRow(ctx, `SELECT payload FROM connection_log_events WHERE payload->'boundary'->>'action'='disabled' LIMIT 1`).Scan(&raw))
			var invalid nodeprov.HistoryEvent
			check(json.Unmarshal(raw, &invalid))
			invalid.ObserverBootID, err = nodeprov.NewHistoryUUID()
			check(err)
			invalid.Sequence, invalid.EventID = 1, string(invalid.ObserverBootID)+":1"
			invalid.Kind, invalid.ProducerID, invalid.Runtime, invalid.Boundary = "collection_gap", "agent", "agent", nil
			invalid.Gap = &nodeprov.HistoryGap{ProducerID: "xray", ObserverBootID: invalid.ObserverBootID, Reason: "producer_lost"}
			invalid.Quality.Completeness = "unknown"
			id, err := nodeprov.NewHistoryUUID()
			check(err)
			digest, err := nodeprov.HistoryEventsDigest([]nodeprov.HistoryEvent{invalid})
			check(err)
			body, err := json.Marshal(map[string]any{"schema_version": 1, "batch_id": id, "payload_sha256": digest, "events": []nodeprov.HistoryEvent{invalid}})
			check(err)
			req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/v1/nodes/"+node+"/connection-history", bytes.NewReader(body))
			check(err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Node-Key", "b1-key")
			response, err := http.DefaultClient.Do(req)
			check(err)
			data, err := io.ReadAll(response.Body)
			check(err)
			check(response.Body.Close())
			if response.StatusCode != 409 || !bytes.Contains(data, []byte("revision_disabled")) {
				t.Fatalf("strict disabled admission weakened: %d %s", response.StatusCode, data)
			}
			t.Logf("B1_NEGATIVE_CONTROL status=%d body=%s", response.StatusCode, data)
		})
	}
}
