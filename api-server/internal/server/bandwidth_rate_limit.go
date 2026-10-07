package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bandwidthNodeID(c *fiber.Ctx) string {
	var suffix string
	switch c.Method() {
	case fiber.MethodPost:
		suffix = "/bandwidth/permit"
	case fiber.MethodGet:
		suffix = "/revoked-devices"
	default:
		return ""
	}
	const prefix = "/api/v1/nodes/"
	path := c.Path()
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	parsed, err := uuid.Parse(id)
	if err != nil || strings.Contains(id, "/") {
		return ""
	}
	return parsed.String()
}

// High-frequency permit and revocation polling share the authentication guard.
// Exemption is based on prior successful authentication, never merely presence
// of a supplied key. Unknown keys are rate-limited before touching PostgreSQL.
func isBandwidthPermitRequest(c *fiber.Ctx) bool {
	id := bandwidthNodeID(c)
	return id != "" && c.Locals("bandwidth_authenticated_node") == id
}

type permitAuthCache struct {
	mu    sync.Mutex
	valid map[[32]byte]time.Time
}

func (cache *permitAuthCache) key(c *fiber.Ctx) [32]byte {
	return sha256.Sum256([]byte(bandwidthNodeID(c) + "\x00" + c.Get("X-Node-Key")))
}
func (cache *permitAuthCache) known(c *fiber.Ctx) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	until := cache.valid[cache.key(c)]
	return time.Now().Before(until)
}

func bandwidthAuthentication(db *pgxpool.Pool) (fiber.Handler, fiber.Handler) {
	cache := &permitAuthCache{valid: make(map[[32]byte]time.Time)}
	guard := limiter.New(limiter.Config{
		Next: func(c *fiber.Ctx) bool { return bandwidthNodeID(c) == "" || cache.known(c) },
		Max:  100, Expiration: time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string { return c.IP() },
	})
	authenticate := func(c *fiber.Ctx) error {
		id := bandwidthNodeID(c)
		if id == "" {
			return c.Next()
		}
		key := c.Get("X-Node-Key")
		if key == "" || len(key) > 1024 {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		if !cache.known(c) {
			if db == nil {
				return c.SendStatus(fiber.StatusUnauthorized)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var stored string
			err := db.QueryRow(ctx, `SELECT api_key FROM nodes WHERE id=$1 AND status<>'pending'`, id).Scan(&stored)
			if err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(key)) != 1 {
				return c.SendStatus(fiber.StatusUnauthorized)
			}
			cache.mu.Lock()
			// Cache only successful node authentication, not entitlement or byte grants.
			// Bound memory even if credentials churn during control-plane maintenance.
			if len(cache.valid) >= 4096 {
				for k, until := range cache.valid {
					if !time.Now().Before(until) {
						delete(cache.valid, k)
					}
				}
			}
			if len(cache.valid) < 4096 {
				cache.valid[cache.key(c)] = time.Now().Add(5 * time.Second)
			}
			cache.mu.Unlock()
		}
		c.Locals("bandwidth_authenticated_node", id)
		return c.Next()
	}
	return guard, authenticate
}
