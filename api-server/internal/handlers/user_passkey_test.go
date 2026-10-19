package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
	"github.com/redis/go-redis/v9"
)

func passkeyTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}
func TestPasskeyListAndDeleteOnlyOwnCredentials(t *testing.T) {
	p := passkeyTestDB(t)
	ctx := context.Background()
	email := "passkey-test-" + crypto.NewUUID() + "@example.test"
	var uid string
	err := p.QueryRow(ctx, `INSERT INTO users(email,name,password_hash,sub_token,status) VALUES($1,'passkey test','unused',$2,'pending') RETURNING id::text`, email, crypto.NewUUID()).Scan(&uid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = p.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid) })
	rdb := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR")})
	t.Cleanup(func() { _ = rdb.Close() })
	if err = rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("TEST_REDIS_ADDR unavailable: %v", err)
	}
	wa, err := webauthn.New(&webauthn.Config{RPID: "localhost", RPDisplayName: "Proxima", RPOrigins: []string{"http://localhost:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewUserAuthHandler(p, "test-secret", time.Hour, services.NewTelegramService(p, config.TelegramConfig{}), nil, "")
	h.SetPasskeyServices(rdb, wa, false)
	app := fiber.New()
	protected := app.Group("/user", func(c *fiber.Ctx) error { c.Locals("user_id", uid); return c.Next() })
	protected.Get("/passkeys", h.ListPasskeys)
	protected.Delete("/passkeys/:id", h.DeletePasskey)
	res, err := app.Test(httptest.NewRequest("GET", "/user/passkeys", nil))
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	_ = json.NewDecoder(res.Body).Decode(&list)
	_ = res.Body.Close()
	if res.StatusCode != 200 || len(list) != 0 {
		t.Fatalf("list status=%d result=%v", res.StatusCode, list)
	}
	res, err = app.Test(httptest.NewRequest("DELETE", "/user/passkeys/00000000-0000-0000-0000-000000000001", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("delete unknown = %d", res.StatusCode)
	}
}
