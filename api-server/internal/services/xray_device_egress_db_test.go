package services

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// Exercise the production GenerateConfig/GenerateDigest path when a migrated
// PostgreSQL test database is available; pure declaration tests need no DB.
func TestDeviceEgressGenerateConfigAndDigest(t *testing.T) {
	pool, ctx := openEvictionPool(t)
	nodeID, limitedA := seedEvictionFixture(t, ctx, pool)
	var limitedUserID, limitedPlanID, groupID string
	if err := pool.QueryRow(ctx,
		`SELECT u.id::text, p.id::text, p.node_group_id::text
		 FROM devices d JOIN users u ON u.id = d.user_id JOIN plans p ON p.id = u.plan_id
		 WHERE d.xray_uuid = $1`, limitedA,
	).Scan(&limitedUserID, &limitedPlanID, &groupID); err != nil {
		t.Fatalf("read limited fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE plans SET speed_limit = 25 WHERE id = $1`, limitedPlanID); err != nil {
		t.Fatalf("limit plan: %v", err)
	}
	var limitedB string
	if err := pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid) VALUES ($1, 'second-limited', gen_random_uuid()) RETURNING xray_uuid::text`, limitedUserID,
	).Scan(&limitedB); err != nil {
		t.Fatalf("seed same-tier device: %v", err)
	}

	var unlimitedPlanID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		 VALUES ($1, 30, 5, $2) RETURNING id::text`, "unlimited-"+limitedA, groupID,
	).Scan(&unlimitedPlanID); err != nil {
		t.Fatalf("seed unlimited plan: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, unlimitedPlanID) })
	var unlimitedUserID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, plan_id, status, is_active, sub_token)
		 VALUES ($1, 'x', $2, 'active', true, $3) RETURNING id::text`,
		limitedA+"@unlimited.example.com", unlimitedPlanID, "unlimited-"+limitedA,
	).Scan(&unlimitedUserID); err != nil {
		t.Fatalf("seed unlimited user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM devices WHERE user_id = $1`, unlimitedUserID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, unlimitedUserID)
	})
	var unlimitedID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid) VALUES ($1, 'unlimited', gen_random_uuid()) RETURNING xray_uuid::text`, unlimitedUserID,
	).Scan(&unlimitedID); err != nil {
		t.Fatalf("seed unlimited device: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE nodes SET tls_cert_file = '/cert.pem', tls_key_file = '/key.pem' WHERE id = $1`, nodeID,
	); err != nil {
		t.Fatalf("enable TLS in fixture: %v", err)
	}
	// The migrated schema allows one configured protocol per node. Mixed
	// protocol exclusion is covered at the generator's pure declaration seam;
	// this fixture exercises the actual VLESS node contract.

	service := NewXrayConfigService(pool)
	config, err := service.GenerateConfig(ctx, nodeID)
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	var cfg xrayConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		t.Fatal(err)
	}
	limitedIDs := []string{limitedA, limitedB}
	sort.Strings(limitedIDs)
	allIDs := append(append([]string{}, limitedIDs...), unlimitedID)
	sort.Strings(allIDs)
	assertDeviceEgressDeclarations(t, cfg, allIDs, []string{"vless-in", "vless-reality-limit-25"})

	seen := map[string]bool{}
	for _, ib := range cfg.Inbounds {
		seen[ib.Tag] = true
		switch ib.Protocol {
		case "vless":
			var settings xrayInboundSettings
			if err := json.Unmarshal(ib.Settings, &settings); err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(settings.Clients))
			for _, c := range settings.Clients {
				ids = append(ids, c.ID)
			}
			want := append([]string{}, limitedIDs...)
			if ib.Tag == "vless-in" {
				want = append(want, unlimitedID)
				if ib.Port != 8443 {
					t.Errorf("main VLESS port changed: %d", ib.Port)
				}
			} else if ib.Tag == "vless-reality-limit-25" && ib.Port != 20025 {
				t.Errorf("legacy tier port changed: %d", ib.Port)
			}
			sort.Strings(want)
			if !reflect.DeepEqual(ids, want) {
				t.Errorf("%s clients = %v, want sorted %v", ib.Tag, ids, want)
			}
		case "vmess":
			var settings xrayVmessInboundSettings
			if err := json.Unmarshal(ib.Settings, &settings); err != nil {
				t.Fatal(err)
			}
			if len(settings.Clients) != 1 || settings.Clients[0].ID != unlimitedID {
				t.Errorf("limited devices received shared VMess credentials: %+v", settings.Clients)
			}
		case "trojan":
			var settings xrayTrojanInboundSettings
			if err := json.Unmarshal(ib.Settings, &settings); err != nil {
				t.Fatal(err)
			}
			if len(settings.Clients) != 1 || settings.Clients[0].Password != unlimitedID {
				t.Errorf("limited devices received shared Trojan credentials: %+v", settings.Clients)
			}
		}
	}
	for _, tag := range []string{"vless-in", "vless-reality-limit-25"} {
		if !seen[tag] {
			t.Errorf("missing inbound %s", tag)
		}
	}
	second, err := service.GenerateConfig(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(config, second) {
		t.Error("unchanged DB state produced different canonical configs")
	}
	before, err := service.GenerateDigest(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE devices SET evicted_until = NOW() + INTERVAL '10 minutes' WHERE xray_uuid = $1`, limitedB); err != nil {
		t.Fatalf("evict limited device: %v", err)
	}
	after, err := service.GenerateDigest(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if before.StructureHash == after.StructureHash || before.Hash == after.Hash {
		t.Error("limited device removal failed to require a full config reload")
	}
	if _, err := pool.Exec(ctx, `UPDATE devices SET evicted_until = NOW() + INTERVAL '10 minutes' WHERE xray_uuid = $1`, unlimitedID); err != nil {
		t.Fatalf("evict unlimited device: %v", err)
	}
	withoutUnlimited, err := service.GenerateDigest(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StructureHash == withoutUnlimited.StructureHash {
		t.Error("unlimited VLESS device removal did not change structure hash")
	}
}

func TestDeviceEgressGenerateConfigCompatibilityPortCollision(t *testing.T) {
	for _, mode := range []string{"legacy", "explicit", "incompatible"} {
		t.Run(mode, func(t *testing.T) {
			pool, ctx := openEvictionPool(t)
			nodeID, deviceID := seedEvictionFixture(t, ctx, pool)
			if _, err := pool.Exec(ctx,
				`UPDATE plans SET speed_limit = 25 WHERE id =
				 (SELECT u.plan_id FROM users u JOIN devices d ON d.user_id = u.id WHERE d.xray_uuid = $1)`, deviceID,
			); err != nil {
				t.Fatalf("limit plan: %v", err)
			}
			if _, err := pool.Exec(ctx, `UPDATE nodes SET port = 20025 WHERE id = $1`, nodeID); err != nil {
				t.Fatalf("set main port: %v", err)
			}
			tag := "vless-in"
			switch mode {
			case "legacy":
				if _, err := pool.Exec(ctx, `DELETE FROM inbounds WHERE node_id = $1`, nodeID); err != nil {
					t.Fatal(err)
				}
				tag = "vless-reality"
			case "explicit":
				if _, err := pool.Exec(ctx, `UPDATE inbounds SET port = 20025 WHERE node_id = $1`, nodeID); err != nil {
					t.Fatal(err)
				}
			case "incompatible":
				if _, err := pool.Exec(ctx,
					`UPDATE inbounds SET protocol = 'shadowsocks', port = 20025,
					 settings = '{"method":"aes-128-gcm","password":"fixture"}'::jsonb WHERE node_id = $1`, nodeID,
				); err != nil {
					t.Fatal(err)
				}
			}
			config, err := NewXrayConfigService(pool).GenerateConfig(ctx, nodeID)
			if mode == "incompatible" {
				if err == nil {
					t.Fatal("incompatible occupied tier port silently generated a config")
				}
				return
			}
			if err != nil {
				t.Fatalf("GenerateConfig: %v", err)
			}
			var cfg xrayConfig
			if err := json.Unmarshal(config, &cfg); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, ib := range cfg.Inbounds {
				if ib.Port == 20025 {
					count++
					if ib.Tag != tag {
						t.Errorf("reused listener tag = %s, want %s", ib.Tag, tag)
					}
				}
			}
			if count != 1 {
				t.Fatalf("got %d listeners on compatibility port 20025, want one", count)
			}
			assertDeviceEgressDeclarations(t, cfg, []string{deviceID}, []string{tag})
		})
	}
}
