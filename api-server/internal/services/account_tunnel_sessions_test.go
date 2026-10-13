package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/nodeprov"
)

func seedSessionExit(t *testing.T, ctx context.Context, db *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := db.QueryRow(ctx, `INSERT INTO nodes(name,ip,api_key,role,status,last_seen) VALUES($1,'198.51.100.1',$2,'exit','online',NOW()) RETURNING id::text`, `session-exit-`+uuid.NewString(), uuid.NewString()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(context.Background(), `DELETE FROM nodes WHERE id=$1`, id) })
	return id
}

func TestAccountTunnelSessionsCanonicalUUIDOwnership(t *testing.T) {
	db, rdb, _, ctx := setupTracker(t)
	user, credentials := seedUserWithDevices(t, ctx, db, 1)
	exit := seedSessionExit(t, ctx, db)
	defer func() { _ = rdb.Del(context.Background(), "node:"+exit+":tunnels").Err() }()
	connection := nodeprov.TunnelConnection{UUID: strings.ToUpper(credentials[0]), PeerIP: "203.0.113.7", PeerPort: 41000, LocalIP: "10.0.2.9", LocalPort: 8443, AcceptedAt: time.Now().UnixMilli()}
	if err := PublishTunnelReport(ctx, rdb, exit, nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{connection}}); err != nil {
		t.Fatal(err)
	}
	observation, snapshot, err := ObserveAccountTunnelSessions(ctx, db, rdb, user)
	if err != nil || !observation.Complete || snapshot == "" || len(observation.Sessions) != 1 {
		t.Fatalf("uppercase UUID report omitted live session: %+v snapshot=%q err=%v", observation, snapshot, err)
	}

	connection.UUID = "not-a-uuid"
	if err := PublishTunnelReport(ctx, rdb, exit, nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{connection}}); err != nil {
		t.Fatal(err)
	}
	observation, snapshot, err = ObserveAccountTunnelSessions(ctx, db, rdb, user)
	if err != nil || observation.Complete || snapshot != "" {
		t.Fatalf("malformed UUID report did not fail closed: %+v snapshot=%q err=%v", observation, snapshot, err)
	}
}

func TestAccountTunnelSessionsCountTransportsAcrossExits(t *testing.T) {
	db, rdb, _, ctx := setupTracker(t)
	user, credentials := seedUserWithDevices(t, ctx, db, 1)
	exits := []string{seedSessionExit(t, ctx, db), seedSessionExit(t, ctx, db)}
	now := time.Now()
	c := nodeprov.TunnelConnection{UUID: credentials[0], PeerIP: "203.0.113.2", PeerPort: 40000, LocalIP: "10.0.2.2", LocalPort: 8443, AcceptedAt: now.UnixMilli()}
	for n, id := range exits {
		connection := c
		connection.PeerPort += n
		if err := PublishTunnelReport(ctx, rdb, id, nodeprov.TunnelReport{Known: true, Connections: []nodeprov.TunnelConnection{connection}}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rdb.Del(context.Background(), "node:"+id+":tunnels").Err() })
	}
	obs, snapshot, err := ObserveAccountTunnelSessions(ctx, db, rdb, user)
	if err != nil || !obs.Complete || snapshot == "" {
		t.Fatalf("obs=%+v snapshot=%s err=%v", obs, snapshot, err)
	}
	count, err := CountTunnelSessions(obs)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := PublishTunnelReport(ctx, rdb, exits[1], nodeprov.TunnelReport{}); err != nil {
		t.Fatal(err)
	}
	obs, snapshot, err = ObserveAccountTunnelSessions(ctx, db, rdb, user)
	if err != nil || obs.Complete || snapshot != "" {
		t.Fatalf("unknown Exit reported complete: %+v %q %v", obs, snapshot, err)
	}

}
