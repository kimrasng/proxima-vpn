package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// NewPostgresPool creates a connection pool to PostgreSQL and verifies connectivity.
func NewPostgresPool(ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}

	if cfg.MaxConnections > 0 {
		poolCfg.MaxConns = int32(cfg.MaxConnections)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}
