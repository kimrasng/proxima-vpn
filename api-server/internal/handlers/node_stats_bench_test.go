package handlers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// Run against a disposable PostgreSQL only: each iteration persists 100 real
// device rows and user charges. No Xray or Redis performance is inferred.
func BenchmarkAccountStats100Devices(b *testing.B) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		b.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		b.Fatal(err)
	}
	var nodeID, groupID, planID, userID string
	suffix := crypto.NewUUID()
	if err := pool.QueryRow(ctx, `INSERT INTO nodes (name, api_key, ip, role, status)
		VALUES ($1, 'bench', '203.0.113.50'::inet, 'exit', 'offline') RETURNING id::text`, "bench-node-"+suffix).Scan(&nodeID); err != nil {
		b.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO node_groups (name) VALUES ($1) RETURNING id::text`, "bench-group-"+suffix).Scan(&groupID); err != nil {
		b.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO plans (name, duration_days, max_devices, node_group_id)
		VALUES ($1, 30, 100, $2) RETURNING id::text`, "bench-plan-"+suffix, groupID).Scan(&planID); err != nil {
		b.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, sub_token, plan_id)
		VALUES ($1, 'unused', $2, $3) RETURNING id::text`, "bench-"+suffix+"@example.test", suffix, planID).Scan(&userID); err != nil {
		b.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM traffic_logs WHERE node_id=$1`, nodeID)
		_, _ = pool.Exec(ctx, `DELETE FROM traffic_batches WHERE node_id=$1`, nodeID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id=$1`, planID)
		_, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id=$1`, groupID)
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id=$1`, nodeID)
	}()
	stats := make([]statEntry, 100)
	for i := range stats {
		stats[i] = statEntry{XrayUUID: crypto.NewUUID(), UpBytes: 12345, DnBytes: 67890}
		if _, err := pool.Exec(ctx, `INSERT INTO devices (user_id, xray_uuid) VALUES ($1,$2)`, userID, stats[i].XrayUUID); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := accountStats(ctx, pool, nodeID, crypto.NewUUID(), stats); err != nil {
			b.Fatal(fmt.Errorf("batch %d: %w", i, err))
		}
	}
}
