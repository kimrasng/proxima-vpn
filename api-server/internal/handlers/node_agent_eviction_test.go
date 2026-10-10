package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestUUIDEvictionAcknowledgments(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	n1, n2, foreign, device, epoch := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	for _, node := range []string{n1, n2, foreign} {
		if _, err := db.Exec(ctx, `INSERT INTO nodes(id,name,ip,role,api_key,status) VALUES($1,'eviction-test','203.0.113.7','exit','key','online')`, node); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = db.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, node) }()
	}
	if _, err := db.Exec(ctx, `INSERT INTO uuid_evictions(device_uuid,epoch,required_node_ids) VALUES($1,$2,ARRAY[$3,$4]::uuid[])`, device, epoch, n1, n2); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM uuid_evictions WHERE device_uuid=$1`, device) }()
	h := &NodeAgentHandler{db: db}
	app := fiber.New()
	app.Get("/nodes/:id/revoked-devices", func(c *fiber.Ctx) error { c.Locals("node_id", c.Params("id")); return h.RevokedDevices(c) })
	app.Post("/nodes/:id/revoked-devices/ack", func(c *fiber.Ctx) error { c.Locals("node_id", n1); return h.AcknowledgeRevocation(c) })
	req := httptest.NewRequest("GET", "/nodes/"+n1+"/revoked-devices", nil)
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var snapshot struct {
		Revoked     []string `json:"revoked_uuids"`
		Revocations []struct {
			UUID  string `json:"uuid"`
			Epoch string `json:"epoch"`
		} `json:"revocations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range snapshot.Revocations {
		if r.UUID == device && r.Epoch == epoch {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing pending eviction %+v", snapshot)
	}
	post := func(node, e string) int {
		t.Helper()
		r := httptest.NewRequest("POST", "/nodes/"+node+"/revoked-devices/ack", strings.NewReader(`{"uuid":"`+device+`","epoch":"`+e+`"}`))
		r.Header.Set("Content-Type", "application/json")
		response, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode
	}
	if got := post(foreign, epoch); got != 401 {
		t.Fatalf("spoof status %d", got)
	}
	if got := post(n1, crypto.NewUUID()); got != 409 {
		t.Fatalf("stale status %d", got)
	}
	if got := post(n1, epoch); got != 204 {
		t.Fatalf("ack status %d", got)
	}
	if got := post(n1, epoch); got != 204 {
		t.Fatalf("idempotent status %d", got)
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT status FROM uuid_evictions WHERE device_uuid=$1`, device).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("premature confirmation %q %v", status, err)
	}
	if err := database.AcknowledgeUUIDEviction(ctx, db, device, epoch, n2); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT status FROM uuid_evictions WHERE device_uuid=$1`, device).Scan(&status); err != nil || status != "confirmed" {
		t.Fatalf("confirmation %q %v", status, err)
	}
}
