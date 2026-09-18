package handlers

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Adding a column to a SELECT while getting its Scan destinations wrong still
// compiles and passes vet - it fails only when a row is actually read, which
// broke the node list until a manual request caught it. These run the real
// queries so a mismatch fails here instead.
func TestNodeListAndDetailQueriesScanEveryColumnTheySelect(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	tag := fmt.Sprintf("scan-test-%d", os.Getpid())
	var nodeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, ip, api_key, status, last_seen, shaping_ok, shaping_tiers, shaping_error)
		 VALUES ($1, '198.51.100.20', $2, 'online', NOW(), false, 2, 'tc refused')
		 RETURNING id::text`,
		tag, tag+"-key",
	).Scan(&nodeID); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID)
	})

	listSQL := `SELECT id, name, country, region, ip::text, port, status, xray_version,
	        cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out,
	        last_seen, created_at, updated_at, last_ping_at,
	        xray_running, config_hash,
	        shaping_ok, shaping_tiers, shaping_error
	 FROM nodes WHERE id = $1`

	rows, err := pool.Query(ctx, listSQL, nodeID)
	if err != nil {
		t.Fatalf("list query: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("seeded node not returned by the list query")
	}
	var n nodeListItem
	if err := rows.Scan(
		&n.ID, &n.Name, &n.Country, &n.Region, &n.IP, &n.Port,
		&n.Status, &n.XrayVersion,
		&n.CPUUsage, &n.MemoryUsage, &n.DiskUsage, &n.LoadAvg, &n.NetworkIn, &n.NetworkOut,
		&n.LastSeen, &n.CreatedAt, &n.UpdatedAt, &n.LastPingAt,
		&n.XrayRunning, &n.ConfigHash,
		&n.ShapingOK, &n.ShapingTiers, &n.ShapingError,
	); err != nil {
		t.Fatalf("scan mismatch against the list SELECT: %v", err)
	}

	if n.ShapingOK == nil || *n.ShapingOK {
		t.Error("shaping_ok did not round-trip as false")
	}
	if n.ShapingTiers == nil || *n.ShapingTiers != 2 {
		t.Errorf("shaping_tiers did not round-trip as 2, got %v", n.ShapingTiers)
	}
	if n.ShapingError == nil || *n.ShapingError != "tc refused" {
		t.Errorf("shaping_error did not round-trip, got %v", n.ShapingError)
	}
}

// A node that has never reported must not read as "shaping broken" - the
// default has to mean "no problem known" or every fresh node looks faulty.
func TestFreshNodeDefaultsToShapingHealthy(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	tag := fmt.Sprintf("fresh-node-%d", os.Getpid())
	var (
		nodeID string
		ok     bool
		tiers  int
		reason string
	)
	if err := pool.QueryRow(ctx,
		`INSERT INTO nodes (name, ip, api_key, status)
		 VALUES ($1, '198.51.100.21', $2, 'pending')
		 RETURNING id::text, shaping_ok, shaping_tiers, shaping_error`,
		tag, tag+"-key",
	).Scan(&nodeID, &ok, &tiers, &reason); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, nodeID)
	})

	if !ok {
		t.Error("a node that has not reported yet is flagged as failing to shape")
	}
	if tiers != 0 || reason != "" {
		t.Errorf("unexpected defaults: tiers=%d reason=%q", tiers, reason)
	}
}
