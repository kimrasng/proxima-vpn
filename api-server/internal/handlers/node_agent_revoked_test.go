package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestRevokedDevicesNodeScopedSnapshot(t *testing.T) {
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
	node, grp, plan, user, device, other := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	_, err = db.Exec(ctx, `INSERT INTO nodes(id,name,ip,role,api_key,status) VALUES($1,'revocation-exit','203.0.113.5','exit','secret','online')`, node)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, node) }()
	_, err = db.Exec(ctx, `INSERT INTO node_groups(id,name) VALUES($1,'revocation-group')`, grp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM node_groups WHERE id=$1`, grp) }()
	_, err = db.Exec(ctx, `INSERT INTO node_group_nodes(node_group_id,node_id) VALUES($1,$2)`, grp, node)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO plans(id,name,duration_days,max_devices,node_group_id) VALUES($1,'revoked',30,2,$2)`, plan, grp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM plans WHERE id=$1`, plan) }()
	_, err = db.Exec(ctx, `INSERT INTO users(id,email,password_hash,sub_token,plan_id,status) VALUES($1,$2,'x',$3,$4,'active')`, user, user+"@invalid.test", crypto.NewUUID(), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(ctx, `DELETE FROM users WHERE id=$1`, user) }()
	_, err = db.Exec(ctx, `INSERT INTO devices(user_id,xray_uuid,evicted_until) VALUES($1,$2,NOW()+INTERVAL '10 minutes'),($1,$3,NULL)`, user, device, other)
	if err != nil {
		t.Fatal(err)
	}
	// Move the account away from this Exit; a previous session still running
	// here must remain revoked even though current group membership changed.
	if _, err := db.Exec(ctx, `DELETE FROM node_group_nodes WHERE node_group_id=$1 AND node_id=$2`, grp, node); err != nil {
		t.Fatal(err)
	}
	h := NewNodeAgentHandler(db, nil, services.ManagedEntryDNSIntentConfig{})
	app := fiber.New()
	app.Get("/nodes/:id/revoked-devices", func(c *fiber.Ctx) error { c.Locals("node_id", node); return h.RevokedDevices(c) })
	req := httptest.NewRequest("GET", "/nodes/"+node+"/revoked-devices", nil)
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var got struct {
		RevokedUUIDs []string `json:"revoked_uuids"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || len(got.RevokedUUIDs) != 1 || got.RevokedUUIDs[0] != device {
		t.Fatalf("revoked status=%d got=%+v", res.StatusCode, got)
	}
}
