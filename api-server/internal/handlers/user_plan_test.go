package handlers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The user-facing node list must never carry anything a client could use to
// reach a node directly, or anything about how it is configured. Marshalling the
// struct and inspecting the keys is the check that matters: a field added to
// userNodeItem is published to every authenticated user, and reviewing the SQL
// alone would not catch it.
func TestUserNodeItemExposesOnlyNameAndLocation(t *testing.T) {
	body, err := json.Marshal(userNodeItem{
		Name: "seoul-1", Country: "KR", Region: "seoul", Status: "online",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	allowed := map[string]bool{"name": true, "country": true, "region": true, "status": true}
	for key := range decoded {
		if !allowed[key] {
			t.Errorf("unexpected key %q in the user node response: %s", key, body)
		}
	}
	for key := range allowed {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing key %q: %s", key, body)
		}
	}
}

// Guards against a field being added under a different json name than the Go
// field: the payload is scanned for anything resembling an address, credential
// or protocol detail.
func TestUserNodeResponseCarriesNoConnectionDetail(t *testing.T) {
	body, err := json.Marshal([]userNodeItem{{
		Name: "seoul-1", Country: "KR", Region: "seoul", Status: "online",
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := strings.ToLower(string(body))

	for _, forbidden := range []string{
		"ip", "port", "key", "reality", "short_id", "shortid",
		"password", "uuid", "api_key", "cert", "domain", "hash",
	} {
		if strings.Contains(payload, forbidden) {
			t.Errorf("payload leaks %q: %s", forbidden, body)
		}
	}
}

// The query must follow the same plan -> node group -> node chain the Xray
// config is generated from, so a user cannot be shown a node they have no
// access to. Executed against a live schema because column and join errors only
// surface at plan time.
func TestUserNodeQueryIsScopedToThePlansGroup(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed handler tests")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	// The statement from UserPlanHandler.ListNodes, kept in sync by hand.
	const q = `SELECT n.name, n.country, n.region, n.status
	           FROM nodes n
	           JOIN node_group_nodes ngn ON ngn.node_id = n.id
	           JOIN node_groups ng ON ng.id = ngn.node_group_id
	           JOIN plans p ON p.node_group_id = ng.id
	           JOIN users u ON u.plan_id = p.id
	           WHERE u.id = $1 AND n.status <> 'pending'
	           ORDER BY n.country, n.name`

	if _, err := pool.Exec(context.Background(), q, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Fatalf("user node query rejected by postgres: %v", err)
	}

	// A user with no plan must join to nothing rather than falling back to
	// every node.
	rows, err := pool.Query(context.Background(), q, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("a user id that matches no row returned nodes; the join is not scoping by user")
	}
}
