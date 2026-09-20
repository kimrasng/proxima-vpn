// Command migrate applies the database schema and exits.
//
// The server does this at startup too, but only after it has connected to Redis
// and bound its port, so it cannot be used to migrate a database on its own: it
// either keeps running or fails for reasons that have nothing to do with the
// schema. CI needs the tables before any test runs, and an operator deploying a
// new build may want the migration to land before the service starts taking
// traffic. Both want exactly this and nothing else.
//
// DATABASE_URL is read directly rather than through config.Load, which would
// also generate and persist a JWT secret - a side effect a migration has no
// business having.
package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		log.Fatalf("failed to migrate database: %v", err)
	}

	log.Println("schema applied")
}
