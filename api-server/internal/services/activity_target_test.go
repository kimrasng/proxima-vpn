package services

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The target filter is expressed in SQL, so a wrong predicate or a wrong
// parameter position still compiles and passes vet. These exercise the real
// query against a real table.
func TestActivityListForTarget(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed activity tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)

	svc := NewActivityService(pool)

	run := fmt.Sprintf("activity-target-test-%d", os.Getpid())
	nodeA := run + "-node-a"
	nodeB := run + "-node-b"
	userC := run + "-user-c"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM activity_logs WHERE actor_label = $1`, run)
	})

	seed := []Record{
		{EventType: EventNodeRegistered, ActorLabel: run, TargetType: "node", TargetID: nodeA},
		{EventType: EventNodeOffline, ActorLabel: run, TargetType: "node", TargetID: nodeA},
		{EventType: EventNodeOffline, ActorLabel: run, TargetType: "node", TargetID: nodeB},
		{EventType: EventUserLogin, ActorLabel: run, TargetType: "user", TargetID: userC},
	}
	for _, r := range seed {
		svc.Log(ctx, r)
	}

	countSeeded := func(entries []Entry) int {
		n := 0
		for _, e := range entries {
			if e.ActorLabel == run {
				n++
			}
		}
		return n
	}

	t.Run("filtered request returns only the target entries", func(t *testing.T) {
		entries, err := svc.ListForTarget(ctx, 200, "node", nodeA)
		if err != nil {
			t.Fatalf("ListForTarget: %v", err)
		}
		if got := countSeeded(entries); got != 2 {
			t.Fatalf("expected the 2 entries seeded for nodeA, got %d", got)
		}
		for _, e := range entries {
			if e.TargetType == nil || *e.TargetType != "node" {
				t.Fatalf("entry %s has target_type %v, want node", e.ID, e.TargetType)
			}
			if e.TargetID == nil || *e.TargetID != nodeA {
				t.Fatalf("entry %s has target_id %v, want %s", e.ID, e.TargetID, nodeA)
			}
		}
	})

	t.Run("unfiltered request still returns every target", func(t *testing.T) {
		entries, err := svc.List(ctx, 200)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := countSeeded(entries); got != len(seed) {
			t.Fatalf("expected all %d seeded entries, got %d", len(seed), got)
		}
	})

	t.Run("a half-supplied filter widens instead of matching nothing", func(t *testing.T) {
		entries, err := svc.ListForTarget(ctx, 200, "node", "")
		if err != nil {
			t.Fatalf("ListForTarget: %v", err)
		}
		if got := countSeeded(entries); got != len(seed) {
			t.Fatalf("expected the unfiltered feed of %d entries, got %d", len(seed), got)
		}
	})
}
