package services

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
	"github.com/redis/go-redis/v9"
)

// Each fixture creates and drops its own database. It never migrates, truncates,
// or flushes the configured PostgreSQL/Redis databases.
func relayProofFixture(t *testing.T) (*pgxpool.Pool, *redis.Client) {
	t.Helper()
	dsn, addr := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("TEST_DATABASE_URL and TEST_REDIS_ADDR required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "relay_proof_" + uuid.New().String()[:8]
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(ctx, "DROP DATABASE "+quoted+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	_, err = db.Exec(ctx, `CREATE TABLE nodes(id uuid PRIMARY KEY, role text NOT NULL, ip inet NOT NULL);
 CREATE TABLE node_chains(id uuid PRIMARY KEY, entry_node_id uuid, relay_pool_id uuid, exit_node_id uuid, entry_port int, exit_port int, transport text, mode text, enabled boolean);
 CREATE TABLE node_group_nodes(node_group_id uuid, node_id uuid);`)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, rdb
}

func TestRelayAdmissionProofInvalid(t *testing.T) {
	for _, tc := range []struct {
		exit, peer  string
		port, local int
	}{
		{"bad", "192.0.2.1", 123, 443}, {uuid.NewString(), "host", 123, 443},
		{uuid.NewString(), "0.0.0.0", 123, 443}, {uuid.NewString(), "fe80::1%eth0", 123, 443},
		{uuid.NewString(), "192.0.2.1", 0, 443}, {uuid.NewString(), "192.0.2.1", 123, 65536},
	} {
		if source, err := ResolveRelayAdmissionSource(context.Background(), nil, nil, tc.exit, tc.peer, tc.port, tc.local); err == nil || source != "" {
			t.Fatalf("invalid tuple accepted: %q %v", source, err)
		}
	}
}

func TestRelayAdmissionProof(t *testing.T) {
	db, rdb := relayProofFixture(t)
	ctx := context.Background()
	exit, entry, other, chain, pool := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO nodes VALUES ($1,'exit','198.51.100.10/24'),($2,'relay','192.0.2.10'),($3,'relay','192.0.2.11')`, exit, entry, other)
	exec(`INSERT INTO node_chains VALUES ($1,$2,NULL,$3,30001,443,'tcp','l4_dnat',true)`, chain, entry, exit)
	exec(`INSERT INTO node_group_nodes VALUES ($1,$2),($1,$3)`, pool, entry, other)
	t.Cleanup(func() { rdb.Del(context.Background(), "node:"+entry+":relay_flows", "node:"+other+":relay_flows") })
	base := nodeprov.RelayFlow{PeerIP: "203.0.113.10", PeerPort: 40000, ExitIP: "198.51.100.10", ExitPort: 443, ClientIP: "203.0.113.123"}
	publish := func(id string, flows ...nodeprov.RelayFlow) {
		t.Helper()
		if err := PublishRelayFlows(ctx, rdb, id, nodeprov.RelayFlowReport{Known: true, Flows: flows}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		got, err := ResolveRelayAdmissionSource(ctx, db, rdb, exit, "::ffff:"+base.PeerIP, base.PeerPort, base.ExitPort)
		if want == "" {
			if err == nil || got != "" {
				t.Fatalf("expected rejection: %q %v", got, err)
			}
		} else if err != nil || got != want {
			t.Fatalf("got %q %v, want %q", got, err, want)
		}
	}
	key := "node:" + entry + ":relay_flows"
	reset := func() {
		exec(`UPDATE nodes SET role=CASE WHEN id=$1 THEN 'exit' ELSE 'relay' END`, exit)
		exec(`UPDATE node_chains SET entry_node_id=$1,relay_pool_id=NULL,exit_node_id=$2,exit_port=443,transport='tcp',mode='l4_dnat',enabled=true`, entry, exit)
		publish(entry, base)
		rdb.Del(ctx, "node:"+other+":relay_flows")
	}
	t.Run("multihomed and canonical peer", func(t *testing.T) { reset(); check(base.ClientIP) })
	t.Run("duplicate same original", func(t *testing.T) { reset(); publish(entry, base, base); check(base.ClientIP) })
	t.Run("ambiguity within report", func(t *testing.T) {
		reset()
		f := base
		f.ClientIP = "203.0.113.124"
		publish(entry, base, f)
		check("")
	})
	for _, field := range []string{"peer IP", "peer port", "exit IP", "exit port"} {
		t.Run("mismatch "+field, func(t *testing.T) {
			reset()
			f := base
			switch field {
			case "peer IP":
				f.PeerIP = "192.0.2.99"
			case "peer port":
				f.PeerPort++
			case "exit IP":
				f.ExitIP = "198.51.100.11"
			case "exit port":
				f.ExitPort++
			}
			publish(entry, f)
			check("")
		})
	}
	for _, tc := range []struct {
		name, sql string
		args      []any
	}{
		{"disabled", `UPDATE node_chains SET enabled=false`, nil},
		{"wrong mode", `UPDATE node_chains SET mode='wg_tunnel'`, nil},
		{"UDP only", `UPDATE node_chains SET transport='udp'`, nil},
		{"wrong chain port", `UPDATE node_chains SET exit_port=8443`, nil},
		{"wrong Exit", `UPDATE node_chains SET exit_node_id=$1`, []any{other}},
		{"wrong Entry role", `UPDATE nodes SET role='exit' WHERE id=$1`, []any{entry}},
		{"wrong Exit role", `UPDATE nodes SET role='relay' WHERE id=$1`, []any{exit}},
	} {
		t.Run(tc.name, func(t *testing.T) { reset(); exec(tc.sql, tc.args...); check("") })
	}
	t.Run("tcp_udp both roles", func(t *testing.T) {
		reset()
		exec(`UPDATE node_chains SET transport='tcp_udp'`)
		exec(`UPDATE nodes SET role='both'`)
		check(base.ClientIP)
	})
	t.Run("unrelated Entry forbidden", func(t *testing.T) { reset(); rdb.Del(ctx, key); publish(other, base); check("") })
	t.Run("explicit overrides pool", func(t *testing.T) {
		reset()
		exec(`UPDATE node_chains SET relay_pool_id=$1`, pool)
		rdb.Del(ctx, key)
		publish(other, base)
		check("")
	})
	t.Run("legacy pool and cross Entry ambiguity", func(t *testing.T) {
		reset()
		exec(`UPDATE node_chains SET entry_node_id=NULL,relay_pool_id=$1`, pool)
		rdb.Del(ctx, key)
		publish(other, base)
		check(base.ClientIP)
		f := base
		f.ClientIP = "203.0.113.124"
		publish(entry, f)
		check("")
		publish(entry, base)
		check(base.ClientIP)
	})
	for _, tc := range []struct {
		name, data string
		ttl        time.Duration
	}{
		{"malformed JSON", "{", RelayFlowFreshness},
		{"no receipt identity", `{"known":true,"flows":[]}`, RelayFlowFreshness},
		{"unknown report", `{"ReportID":"receipt","known":false,"flows":[]}`, RelayFlowFreshness},
		{"malformed flow", `{"ReportID":"receipt","known":true,"flows":[{"peer_ip":"bad"}]}`, RelayFlowFreshness},
		{"persistent snapshot", `{"ReportID":"receipt","known":true,"flows":[]}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset()
			if err := rdb.Set(ctx, key, tc.data, tc.ttl).Err(); err != nil {
				t.Fatal(err)
			}
			check("")
		})
	}
	t.Run("persistent valid snapshot", func(t *testing.T) {
		reset()
		if err := rdb.Persist(ctx, key).Err(); err != nil {
			t.Fatal(err)
		}
		check("")
	})
	t.Run("overlong TTL", func(t *testing.T) {
		reset()
		if err := rdb.Expire(ctx, key, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		check("")
	})
	t.Run("expired snapshot", func(t *testing.T) {
		reset()
		if err := rdb.PExpire(ctx, key, time.Millisecond).Err(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		check("")
	})
	t.Run("missing Exit", func(t *testing.T) { reset(); exec(`DELETE FROM nodes WHERE id=$1`, exit); check("") })
}
