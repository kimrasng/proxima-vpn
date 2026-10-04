package handlers

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/redis/go-redis/v9"
)

func endpointHTTPFixture(t *testing.T) nodeChainHTTPFixture {
	t.Helper()
	pool := chainTestDB(t)
	rdb := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR")})
	t.Cleanup(func() { _ = rdb.Close() })
	app := fiber.New()
	h := NewAdminNodeHandler(pool, rdb, "")
	app.Get("/admin/nodes", h.ListNodes)
	app.Get("/admin/nodes/:id", h.GetNode)
	app.Put("/admin/nodes/:id", h.UpdateNode)
	t.Cleanup(func() { _ = app.Shutdown() })
	return nodeChainHTTPFixture{t: t, pool: pool, app: app}
}

func endpointObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode endpoint response: %v", err)
	}
	return object
}

func endpointListItem(t *testing.T, body []byte, id string) map[string]any {
	t.Helper()
	var items []map[string]any
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode node list: %v", err)
	}
	for _, item := range items {
		if item["id"] == id {
			return item
		}
	}
	t.Fatalf("node %s missing from list", id)
	return nil
}

func seedManagedEndpoint(t *testing.T, pool *pgxpool.Pool, nodeID, status string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO managed_entry_dns
		(owner_node_id, node_id, hostname, dns_status, error_code)
		VALUES ($1, $1, 'entry.example.test', $2, $3)`, nodeID, status,
		func() *string {
			if status == "error" {
				code := "provider_rejected"
				return &code
			}
			return nil
		}())
	if err != nil {
		t.Fatalf("seed managed endpoint: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, nodeID)
	})
}

func TestAdminNodeEndpointsExposeSafeNullableStateThroughHTTP(t *testing.T) {
	for _, status := range []string{"", "pending", "ready", "conflict", "error", "deleting"} {
		t.Run(status, func(t *testing.T) {
			f := endpointHTTPFixture(t)
			id := seedChainNode(t, f.pool, "endpoint-"+crypto.NewUUID(), "exit", 443)
			if status != "" {
				seedManagedEndpoint(t, f.pool, id, status)
			}
			_, err := f.pool.Exec(context.Background(), `UPDATE nodes SET reality_client_sni='www.cloudflare.com', reality_sni_source='admin', reality_sni_status='valid', reality_private_key='secret-private' WHERE id=$1`, id)
			if err != nil {
				t.Fatal(err)
			}
			listStatus, listBody := f.request(fiber.MethodGet, "/admin/nodes", "")
			detailStatus, detailBody := f.request(fiber.MethodGet, "/admin/nodes/"+id, "")
			if listStatus != 200 || detailStatus != 200 {
				t.Fatalf("list/detail status %d/%d: %s %s", listStatus, detailStatus, listBody, detailBody)
			}
			list := endpointListItem(t, listBody, id)
			detail := endpointObject(t, detailBody)
			for _, key := range []string{"entry_hostname", "entry_dns_status", "entry_dns_error_code", "reality_client_sni", "reality_sni_status", "reality_sni_error_code"} {
				if _, ok := list[key]; !ok {
					t.Errorf("list missing %s", key)
				}
				if list[key] != detail[key] {
					t.Errorf("%s list=%v detail=%v", key, list[key], detail[key])
				}
			}
			if list["reality_client_sni"] != "www.cloudflare.com" || list["reality_sni_status"] != "valid" {
				t.Errorf("SNI projection: %v", list)
			}
			if status == "" {
				for _, key := range []string{"entry_hostname", "entry_dns_status", "entry_dns_error_code", "reality_sni_error_code"} {
					if list[key] != nil {
						t.Errorf("%s = %v, want null", key, list[key])
					}
				}
			} else if list["entry_hostname"] != "entry.example.test" || list["entry_dns_status"] != status {
				t.Errorf("DNS projection: %v", list)
			}
			if status == "error" && list["entry_dns_error_code"] != "provider_rejected" {
				t.Errorf("safe error code: %v", list)
			}
			for _, key := range []string{"owner_node_id", "provider_config", "api_token", "reality_private_key", "cleanup_requested_at", "desired_ipv4", "observed_ipv4"} {
				if _, ok := list[key]; ok {
					t.Errorf("list leaked %s", key)
				}
				if _, ok := detail[key]; ok {
					t.Errorf("detail leaked %s", key)
				}
			}
		})
	}
}

func TestAdminNodeUpdateRejectsReadOnlyDNSKeysEvenWhenNullThroughHTTP(t *testing.T) {
	for _, key := range []string{"entry_hostname", "entry_dns_status", "entry_dns_error_code"} {
		t.Run(key, func(t *testing.T) {
			f := endpointHTTPFixture(t)
			id := seedChainNode(t, f.pool, "unchanged", "exit", 443)
			status, _ := f.request(fiber.MethodPut, "/admin/nodes/"+id, `{"name":"changed","`+key+`":null}`)
			if status != 400 {
				t.Fatalf("status=%d, want 400", status)
			}
			var name string
			if err := f.pool.QueryRow(context.Background(), `SELECT name FROM nodes WHERE id=$1`, id).Scan(&name); err != nil || name != "unchanged" {
				t.Fatalf("name=%q error=%v", name, err)
			}
		})
	}
}

func TestAdminNodeUpdateCanonicalSNIAndRollbackThroughHTTP(t *testing.T) {
	f := endpointHTTPFixture(t)
	id := seedChainNode(t, f.pool, "before", "exit", 443)
	for _, tc := range []struct {
		body   string
		status int
		name   string
	}{
		{`{"name":"after","reality_client_sni":"WWW.CLOUDFLARE.COM."}`, 200, "after"},
		{`{"name":"bad","reality_client_sni":"not a host"}`, 400, "after"},
		{`{"name":"bad","reality_client_sni":"other.example.test"}`, 409, "after"},
		{`{"name":"bad","reality_client_sni":null}`, 400, "after"},
		{`{"name":"bad","reality_client_sni":""}`, 400, "after"},
		{`{"name":"final"}`, 200, "final"},
	} {
		status, body := f.request(fiber.MethodPut, "/admin/nodes/"+id, tc.body)
		if status != tc.status {
			t.Errorf("body=%s status=%d want=%d: %s", tc.body, status, tc.status, body)
		}
		var name string
		var sni *string
		if err := f.pool.QueryRow(context.Background(), `SELECT name, reality_client_sni FROM nodes WHERE id=$1`, id).Scan(&name, &sni); err != nil {
			t.Fatal(err)
		}
		if name != tc.name || sni == nil || *sni != "www.cloudflare.com" {
			t.Errorf("body=%s persisted name=%q sni=%v", tc.body, name, sni)
		}
	}
	status, _ := f.request(fiber.MethodPut, "/admin/nodes/"+crypto.NewUUID(), `{"reality_client_sni":"www.cloudflare.com"}`)
	if status != 404 {
		t.Errorf("missing node status=%d", status)
	}
}

func TestAdminNodeEndpointsIgnoreDetachedIntentThroughHTTP(t *testing.T) {
	f := endpointHTTPFixture(t)
	id := seedChainNode(t, f.pool, "detached-"+crypto.NewUUID(), "exit", 443)
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO managed_entry_dns
		(owner_node_id, node_id, hostname, dns_status) VALUES ($1, NULL, 'detached.example.test', 'deleting')`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM managed_entry_dns WHERE owner_node_id=$1`, id)
	})
	status, body := f.request(fiber.MethodGet, "/admin/nodes/"+id, "")
	if status != 200 {
		t.Fatalf("detail status=%d: %s", status, body)
	}
	object := endpointObject(t, body)
	for _, field := range []string{"entry_hostname", "entry_dns_status", "entry_dns_error_code"} {
		if object[field] != nil {
			t.Errorf("detached %s=%v, want null", field, object[field])
		}
	}
}

func TestAdminNodeUpdateReturns500OnDatabaseFailureThroughHTTP(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	app := fiber.New()
	app.Put("/admin/nodes/:id", NewAdminNodeHandler(pool, nil, "").UpdateNode)
	t.Cleanup(func() { _ = app.Shutdown() })
	f := nodeChainHTTPFixture{t: t, app: app}
	status, body := f.request(fiber.MethodPut, "/admin/nodes/"+crypto.NewUUID(), `{"reality_client_sni":"www.cloudflare.com"}`)
	if status != 500 {
		t.Errorf("closed DB status=%d, want 500: %s", status, body)
	}
}

func TestAdminNodeUpdateRejectsMissingRealityListenerWithoutWritingThroughHTTP(t *testing.T) {
	f := endpointHTTPFixture(t)
	id := seedChainNode(t, f.pool, "unchanged-"+crypto.NewUUID(), "exit", 443)
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO inbounds
		(node_id, protocol, port, tag, settings, enabled)
		VALUES ($1, 'vmess_ws', 443, 'non-reality', '{}', true)`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM inbounds WHERE node_id=$1`, id) })
	status, body := f.request(fiber.MethodPut, "/admin/nodes/"+id, `{"name":"changed","reality_client_sni":"www.cloudflare.com"}`)
	if status != 409 {
		t.Fatalf("status=%d, want 409: %s", status, body)
	}
	var name string
	var sni *string
	if err := f.pool.QueryRow(context.Background(), `SELECT name, reality_client_sni FROM nodes WHERE id=$1`, id).Scan(&name, &sni); err != nil {
		t.Fatal(err)
	}
	if name == "changed" || sni != nil {
		t.Errorf("failed update persisted name=%q sni=%v", name, sni)
	}
}

func TestAdminNodeEndpointsExposeAbsentAndConflictSNIThroughHTTP(t *testing.T) {
	f := endpointHTTPFixture(t)
	id := seedChainNode(t, f.pool, "sni-state-"+crypto.NewUUID(), "exit", 443)
	status, body := f.request(fiber.MethodGet, "/admin/nodes/"+id, "")
	if status != 200 {
		t.Fatalf("absent detail status=%d: %s", status, body)
	}
	absent := endpointObject(t, body)
	for _, key := range []string{"reality_client_sni", "reality_sni_status", "reality_sni_error_code"} {
		if absent[key] != nil {
			t.Errorf("absent %s=%v", key, absent[key])
		}
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE nodes SET reality_sni_source='admin', reality_sni_status='conflict', reality_sni_error_code='listener_mismatch' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	status, body = f.request(fiber.MethodGet, "/admin/nodes/"+id, "")
	if status != 200 {
		t.Fatalf("conflict detail status=%d: %s", status, body)
	}
	conflict := endpointObject(t, body)
	if conflict["reality_client_sni"] != nil || conflict["reality_sni_status"] != "conflict" || conflict["reality_sni_error_code"] != "listener_mismatch" {
		t.Errorf("conflict SNI projection: %v", conflict)
	}
}
