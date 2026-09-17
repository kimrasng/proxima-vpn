package handlers

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Regression: the query behind GET /admin/users/:id selected u.traffic_reset_day,
// a column the schema never had (it has traffic_reset_at). Postgres rejected
// every call, and because the handler mapped any error to 404 the endpoint just
// reported "user not found" for users that plainly existed - so the admin user
// detail view could never load. Executing the real statement is the only way to
// catch a column that does not exist; it type-checks fine as a string.
//
// Runs only when TEST_DATABASE_URL points at a disposable Postgres, matching the
// convention in internal/telegram/bot_test.go.
func TestUserDetailQueryMatchesSchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	// The statement from AdminUserHandler.Get, kept in sync by hand. A bogus id
	// is fine: the point is that Postgres accepts every column reference, which
	// it decides while planning, before any row is matched.
	_, err = pool.Exec(context.Background(),
		`SELECT u.id, u.email, u.name, u.sub_token, u.plan_id, p.name,
		        u.plan_started_at, u.plan_expires_at, u.traffic_used, p.traffic_limit,
		        u.traffic_reset_at, u.is_active, u.status, u.created_at
		 FROM users u
		 LEFT JOIN plans p ON u.plan_id = p.id
		 WHERE u.id = $1`,
		"00000000-0000-0000-0000-000000000000",
	)
	if err != nil {
		t.Fatalf("user detail query rejected by postgres: %v", err)
	}
}

// The columns the detail response claims to expose must exist under the names
// the handler scans, so a rename cannot silently reintroduce the same class of
// bug elsewhere in the statement.
func TestUsersTableHasTheColumnsTheDetailViewReads(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	for _, col := range []string{
		"id", "email", "name", "sub_token", "plan_id",
		"plan_started_at", "plan_expires_at", "traffic_used",
		"traffic_reset_at", "is_active", "status", "created_at",
	} {
		var exists bool
		err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name = 'users' AND column_name = $1
			 )`, col,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("check column %s: %v", col, err)
		}
		if !exists {
			t.Errorf("users.%s is read by the detail view but missing from the schema", col)
		}
	}

	var ghost bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (
		   SELECT 1 FROM information_schema.columns
		   WHERE table_name = 'users' AND column_name = 'traffic_reset_day'
		 )`,
	).Scan(&ghost); err != nil {
		t.Fatalf("check traffic_reset_day: %v", err)
	}
	if ghost {
		t.Error("users.traffic_reset_day exists; the handler was changed to read traffic_reset_at, so reconcile the two")
	}
}

// Regression: the heartbeat writes nodes.xray_running and nodes.config_hash so
// the panel can tell "the agent checked in" from "Xray is actually serving the
// published config", but nodeListItem had no such fields and neither node query
// selected them - the values accumulated in the database and were never
// reachable through the API, so the observability they exist for was
// unavailable. Executing the statements is what catches a column that is
// selected but absent, or present but not selected.
func TestNodeQueriesExposeTheHeartbeatObservabilityColumns(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	// Both statements from AdminNodeHandler, kept in sync by hand.
	for name, q := range map[string]string{
		"ListNodes": `SELECT id, name, country, region, ip::text, port, status, xray_version,
		                     cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out,
		                     last_seen, created_at, updated_at, last_ping_at,
		                     xray_running, config_hash
		              FROM nodes ORDER BY created_at DESC`,
		"GetNode": `SELECT id, name, country, region, ip::text, port, status, xray_version,
		                   cpu_usage, memory_usage, disk_usage, load_avg, network_in, network_out,
		                   last_seen, created_at, updated_at, last_ping_at,
		                   xray_running, config_hash
		            FROM nodes WHERE id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if name == "GetNode" {
				_, err = pool.Exec(context.Background(), q, "00000000-0000-0000-0000-000000000000")
			} else {
				_, err = pool.Exec(context.Background(), q)
			}
			if err != nil {
				t.Fatalf("%s query rejected by postgres: %v", name, err)
			}
		})
	}

	for _, col := range []string{"xray_running", "config_hash"} {
		var exists bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (
			   SELECT 1 FROM information_schema.columns
			   WHERE table_name = 'nodes' AND column_name = $1
			 )`, col,
		).Scan(&exists); err != nil {
			t.Fatalf("check column %s: %v", col, err)
		}
		if !exists {
			t.Errorf("nodes.%s is read by the admin node API but missing from the schema", col)
		}
	}
}

// The SQL test above cannot notice the struct losing the fields, since it runs
// its own copy of the statement. This pins the response shape instead: the two
// keys have to survive marshalling, which is what the panel reads.
func TestNodeListItemExposesHeartbeatFieldsInJSON(t *testing.T) {
	running := true
	hash := "c4e4ff9a570c"
	body, err := json.Marshal(nodeListItem{XrayRunning: &running, ConfigHash: &hash})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for key, want := range map[string]any{"xray_running": true, "config_hash": "c4e4ff9a570c"} {
		got, ok := decoded[key]
		if !ok {
			t.Errorf("%q missing from the node response: %s", key, body)
			continue
		}
		if got != want {
			t.Errorf("%q = %v, want %v", key, got, want)
		}
	}
}
