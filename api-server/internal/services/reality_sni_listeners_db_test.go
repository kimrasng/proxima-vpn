package services

import (
	"context"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/reality"
)

func TestPHS028EffectiveListenerLoaderAgreesWithBackfill(t *testing.T) {
	// Given legacy fallback, a non-Reality enabled inbound, and configured Reality cases.
	for _, tc := range []struct{ name, protocol, settings, expected string }{
		{"legacy", "", "", "www.cloudflare.com"},
		{"non reality", "vmess_ws", `{}`, ""},
		{"configured", "vless_reality", `{"server_names":["Z.example.test","A.example.test"]}`, "a.example.test"},
		{"malformed settings", "vless_reality", `{"server_names":42}`, "www.cloudflare.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := phs028ServiceDB(t)
			node := phs028ServiceNode(t, pool)
			if tc.protocol != "" {
				phs028ServiceInbound(t, pool, node, tc.protocol, tc.settings, true)
			}
			tx := phs028ServiceTx(t, pool)
			locked, err := LockRealityNode(t.Context(), tx, node)
			if err != nil {
				t.Fatal(err)
			}
			// When the service loads the current effective listeners.
			listeners, err := LoadEffectiveRealityListeners(t.Context(), tx, locked)
			if err != nil {
				t.Fatal(err)
			}
			result, err := reality.Intersect(listeners.Listeners)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := database.Migrate(t.Context(), pool); err != nil {
				t.Fatal(err)
			}
			// Then it agrees with WP-3's persisted backfill on the same fixture.
			var stored *string
			if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni FROM nodes WHERE id=$1`, node).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if result.Proposed.String() != tc.expected || (stored == nil && tc.expected != "") || (stored != nil && *stored != tc.expected) {
				t.Fatalf("service=%+v stored=%v expected=%q", result, stored, tc.expected)
			}
		})
	}
}

func TestPHS028EffectiveListenerLoaderIncludesOnlyEligibleSpeedTiers(t *testing.T) {
	// Given an enabled non-Reality inbound and a speed-limited active client.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	phs028ServiceInbound(t, pool, node, "vmess_ws", `{}`, true)
	var group, user string
	if err := pool.QueryRow(t.Context(), `WITH grp AS (INSERT INTO node_groups (name) VALUES (gen_random_uuid()::text) RETURNING id), pl AS (INSERT INTO plans (name, duration_days, speed_limit, node_group_id) SELECT 'tier',30,10,id FROM grp RETURNING id), usr AS (INSERT INTO users (email,password_hash,sub_token,plan_id,status) SELECT gen_random_uuid()::text,'test',gen_random_uuid()::text,id,'active' FROM pl RETURNING id), dev AS (INSERT INTO devices (user_id,xray_uuid) SELECT id,gen_random_uuid()::text FROM usr RETURNING id) SELECT grp.id::text,usr.id::text FROM grp,usr,dev`).Scan(&group, &user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, query := range []string{`DELETE FROM users WHERE id=$1`, `DELETE FROM plans WHERE node_group_id=$1`, `DELETE FROM node_groups WHERE id=$1`} {
			arg := group
			if query == `DELETE FROM users WHERE id=$1` {
				arg = user
			}
			if _, err := pool.Exec(ctx, query, arg); err != nil {
				t.Error(err)
			}
		}
	})
	if _, err := pool.Exec(t.Context(), `INSERT INTO node_group_nodes (node_group_id,node_id) VALUES ($1,$2)`, group, node); err != nil {
		t.Fatal(err)
	}
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	// When loading the effective listeners and comparing with WP-3 backfill.
	listeners, err := LoadEffectiveRealityListeners(t.Context(), tx, locked)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reality.Intersect(listeners.Listeners)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	// Then the speed-tier default is present only because this client is eligible.
	var stored string
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni FROM nodes WHERE id=$1`, node).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if result.Proposed.String() != "www.cloudflare.com" || stored != result.Proposed.String() {
		t.Fatalf("result=%+v stored=%q", result, stored)
	}
}

func TestPHS028ProspectiveCompatibleAndConflictRepairPersistAtomically(t *testing.T) {
	// Given a conflict with no canonical and an effective proposed listener.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	if _, err := pool.Exec(t.Context(), `UPDATE nodes SET reality_sni_source='backfill', reality_sni_status='conflict', reality_sni_error_code='no_common_name' WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	proposed := ProspectiveRealityListeners{Listeners: []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"B.example.test", "a.example.test"}}}}
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	// When the prospective listener repairs the conflict in the same transaction.
	if err := ValidateProspectiveRealityListeners(t.Context(), tx, locked, proposed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then deterministic backfill is stored with cleared conflict metadata.
	var name, source, status string
	var reason *string
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, node).Scan(&name, &source, &status, &reason); err != nil {
		t.Fatal(err)
	}
	if name != "a.example.test" || source != "backfill" || status != "valid" || reason != nil {
		t.Fatalf("state=%q %q %q %v", name, source, status, reason)
	}
}

func TestPHS028ProspectiveRepairClearsConflictForRetainedCanonical(t *testing.T) {
	// Given a retained admin canonical marked conflicting by an earlier listener set.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	if _, err := pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni='Keep.Example.Test', reality_sni_source='admin', reality_sni_status='conflict', reality_sni_error_code='listener_mismatch' WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	// When the proposed listener again accepts that canonical value.
	proposed := ProspectiveRealityListeners{Listeners: []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"keep.example.test"}}}}
	if err := ValidateProspectiveRealityListeners(t.Context(), tx, locked, proposed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then the old canonical and admin source remain, with a valid status and no error.
	var name, source, status string
	var reason *string
	if err := pool.QueryRow(t.Context(), `SELECT reality_client_sni, reality_sni_source, reality_sni_status, reality_sni_error_code FROM nodes WHERE id=$1`, node).Scan(&name, &source, &status, &reason); err != nil {
		t.Fatal(err)
	}
	if name != "Keep.Example.Test" || source != "admin" || status != "valid" || reason != nil {
		t.Fatalf("state=%q %q %q %v", name, source, status, reason)
	}
}

func TestPHS028ProspectiveIncompatiblePreservesCanonicalAndListenerSettingsOnRollback(t *testing.T) {
	// Given an admin canonical and a configured listener.
	pool := phs028ServiceDB(t)
	node := phs028ServiceNode(t, pool)
	phs028ServiceInbound(t, pool, node, "vless_reality", `{"server_names":["old.example.test"]}`, true)
	if _, err := pool.Exec(t.Context(), `UPDATE nodes SET reality_client_sni='old.example.test', reality_sni_source='admin', reality_sni_status='valid' WHERE id=$1`, node); err != nil {
		t.Fatal(err)
	}
	tx := phs028ServiceTx(t, pool)
	locked, err := LockRealityNode(t.Context(), tx, node)
	if err != nil {
		t.Fatal(err)
	}
	// When a proposed listener drops the canonical and the caller rolls back.
	phs028Kind(t, ValidateProspectiveRealityListeners(t.Context(), tx, locked, ProspectiveRealityListeners{Listeners: []reality.Listener{{Enabled: true, Protocol: "vless", Security: "reality", ServerNames: []string{"new.example.test"}}}}), RealitySNIMismatch)
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Then neither the canonical nor the listener settings change.
	var name, settings string
	if err := pool.QueryRow(t.Context(), `SELECT n.reality_client_sni, i.settings::text FROM nodes n JOIN inbounds i ON i.node_id=n.id WHERE n.id=$1`, node).Scan(&name, &settings); err != nil {
		t.Fatal(err)
	}
	if name != "old.example.test" || settings != `{"server_names": ["old.example.test"]}` {
		t.Fatalf("name=%q settings=%q", name, settings)
	}
}
