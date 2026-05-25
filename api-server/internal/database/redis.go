package database

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// NewRedisClient creates a Redis client and verifies connectivity.
func NewRedisClient(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing redis url: %w", err)
	}

	client := redis.NewClient(opts)

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("pinging redis: %w", err)
	}

	return client, nil
}
