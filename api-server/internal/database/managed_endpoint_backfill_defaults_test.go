package database

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPHS028BackfillDistinguishesNoListenerFromLegacyAndMalformedFallback(t *testing.T) {
	pool, ctx := phs028DB(t)
	legacy := phs028Node(t, pool, t.Name()+"-legacy")
	noReality := phs028Node(t, pool, t.Name()+"-other")
	phs028Inbound(t, pool, noReality, "vmess_ws", `{}`, true)
	retained := phs028Node(t, pool, t.Name()+"-retained")
	phs028Inbound(t, pool, retained, "vmess_ws", `{}`, true)
	if _, err := pool.Exec(ctx, `UPDATE nodes SET reality_client_sni = 'Legacy.Example.Test', reality_sni_source = 'admin', reality_sni_status = 'valid' WHERE id = $1`, retained); err != nil {
		t.Fatal(err)
	}
	malformed := phs028Node(t, pool, t.Name()+"-malformed")
	phs028Inbound(t, pool, malformed, "vless_reality", `{"server_names":42}`, true)
	disabled := phs028Node(t, pool, t.Name()+"-disabled")
	phs028Inbound(t, pool, disabled, "vless_reality", `{"server_names":["ignored.example.test"]}`, false)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, legacy), "www.cloudflare.com", "backfill", "valid", "")
	phs028ExpectSNI(t, phs028State(t, pool, noReality), "", "", "not_applicable", "")
	phs028ExpectSNI(t, phs028State(t, pool, retained), "Legacy.Example.Test", "admin", "not_applicable", "")
	phs028ExpectSNI(t, phs028State(t, pool, malformed), "www.cloudflare.com", "backfill", "valid", "")
	phs028ExpectSNI(t, phs028State(t, pool, disabled), "www.cloudflare.com", "backfill", "valid", "")
}

func phs028TierClient(t *testing.T, pool *pgxpool.Pool, node string) string {
	t.Helper()
	ctx := t.Context()
	var group, user string
	if err := pool.QueryRow(ctx, `WITH grp AS (INSERT INTO node_groups (name) VALUES (gen_random_uuid()::text) RETURNING id), pl AS (INSERT INTO plans (name, duration_days, speed_limit, node_group_id) SELECT 'tier', 30, 10, id FROM grp RETURNING id), usr AS (INSERT INTO users (email, password_hash, sub_token, plan_id, status) SELECT gen_random_uuid()::text, 'test', gen_random_uuid()::text, id, 'active' FROM pl RETURNING id), dev AS (INSERT INTO devices (user_id, xray_uuid) SELECT id, gen_random_uuid()::text FROM usr RETURNING id) SELECT grp.id::text, usr.id::text FROM grp, usr, dev`).Scan(&group, &user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user)
		_, _ = pool.Exec(context.Background(), `DELETE FROM plans WHERE node_group_id = $1`, group)
		_, _ = pool.Exec(context.Background(), `DELETE FROM node_groups WHERE id = $1`, group)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO node_group_nodes (node_group_id, node_id) VALUES ($1, $2)`, group, node); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestPHS028BackfillTierDefaultsOnlyForEligibleClientsWithoutVLESS(t *testing.T) {
	for _, scenario := range []struct {
		name, mutation string
		eligible       bool
	}{
		{"active tier", "", true},
		{"inactive user", `UPDATE users SET is_active = false WHERE id = $1`, false},
		{"pending user", `UPDATE users SET status = 'pending' WHERE id = $1`, false},
		{"unlimited plan", `UPDATE plans SET speed_limit = 0 WHERE id = (SELECT plan_id FROM users WHERE id = $1)`, false},
		{"no node membership", `DELETE FROM node_group_nodes WHERE node_group_id = (SELECT p.node_group_id FROM users u JOIN plans p ON p.id = u.plan_id WHERE u.id = $1)`, false},
		{"expired plan", `UPDATE users SET plan_expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, false},
		{"traffic exhausted", `UPDATE users SET traffic_used = 1 WHERE id = $1`, false},
		{"evicted device", `UPDATE devices SET evicted_until = NOW() + INTERVAL '1 hour' WHERE user_id = $1`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			pool, ctx := phs028DB(t)
			node := phs028Node(t, pool, t.Name())
			phs028Inbound(t, pool, node, "vmess_ws", `{}`, true)
			user := phs028TierClient(t, pool, node)
			if scenario.name == "traffic exhausted" {
				if _, err := pool.Exec(ctx, `UPDATE plans SET traffic_limit = 1 WHERE id = (SELECT plan_id FROM users WHERE id = $1)`, user); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.mutation != "" {
				if _, err := pool.Exec(ctx, scenario.mutation, user); err != nil {
					t.Fatal(err)
				}
			}
			if err := Migrate(ctx, pool); err != nil {
				t.Fatal(err)
			}
			if scenario.eligible {
				phs028ExpectSNI(t, phs028State(t, pool, node), "www.cloudflare.com", "backfill", "valid", "")
			} else {
				phs028ExpectSNI(t, phs028State(t, pool, node), "", "", "not_applicable", "")
			}
		})
	}
}

func TestPHS028BackfillTierDuplicatesFirstVLESSNames(t *testing.T) {
	pool, ctx := phs028DB(t)
	node := phs028Node(t, pool, t.Name())
	phs028Inbound(t, pool, node, "vless_reality", `{"server_names":["configured.example.test"]}`, true)
	phs028TierClient(t, pool, node)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	phs028ExpectSNI(t, phs028State(t, pool, node), "configured.example.test", "backfill", "valid", "")
}
