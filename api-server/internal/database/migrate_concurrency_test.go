package database

import (
	"context"
	"testing"
	"time"
)

func Test_Migrate_completes_for_concurrent_callers(t *testing.T) {
	pool := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- Migrate(ctx, pool)
		}()
	}

	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Migrate: %v", err)
		}
	}
}
