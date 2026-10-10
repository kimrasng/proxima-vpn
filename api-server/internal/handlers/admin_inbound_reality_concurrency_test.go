package handlers

import (
	"context"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func TestInboundRealityUpdateSerializesWithCanonicalWrite(t *testing.T) {
	canonical := "a.example.test"
	f := newInboundRealityFixture(t, &canonical)
	id := f.seed("vless_reality", `"a.example.test","b.example.test"`, true)
	ctx := context.Background()
	tx, err := f.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := services.LockRealityNode(ctx, tx, f.node)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.UpdateCanonicalRealitySNI(ctx, tx, locked, "b.example.test"); err != nil {
		t.Fatal(err)
	}

	// Given an uncommitted canonical write holding the node row; When an inbound update starts; Then it waits at the node lock.
	result := make(chan struct {
		status int
		body   string
	}, 1)
	go func() {
		status, body := f.request("PUT", "/inbounds/"+id, `{"settings":{"server_names":["a.example.test"]},"port":8443}`)
		result <- struct {
			status int
			body   string
		}{status, body}
	}()
	waitForBlockedQuery(t, f.db, "FROM nodes", "FOR UPDATE")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.status != 409 {
		t.Fatalf("update after canonical write = %d: %s", got.status, got.body)
	}
	var port int
	if err := f.db.QueryRow(ctx, `SELECT port FROM inbounds WHERE id=$1`, id).Scan(&port); err != nil {
		t.Fatal(err)
	}
	if port != 443 {
		t.Fatalf("rolled-back port = %d", port)
	}
	if name, _, _, _ := f.state(); name != "b.example.test" {
		t.Fatalf("canonical = %q", name)
	}
}
