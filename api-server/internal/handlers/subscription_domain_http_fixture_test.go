package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type subscriptionDomainFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	app  *fiber.App
}

func newSubscriptionDomainFixture(t *testing.T) subscriptionDomainFixture {
	t.Helper()
	pool := chainTestDB(t)
	if _, err := pool.Exec(context.Background(), `DELETE FROM subscription_domains`); err != nil {
		t.Fatalf("clear isolated subscription domains: %v", err)
	}
	app := fiber.New()
	admin := NewAdminSubscriptionDomainHandler(pool)
	app.Get("/admin/subscription-domains", admin.List)
	app.Post("/admin/subscription-domains", admin.Create)
	app.Put("/admin/subscription-domains/:id", admin.Update)
	app.Get("/admin/subscription-domains/health", admin.Health)
	app.Get("/user/subscription-domains", NewUserSubscriptionDomainHandler(pool).List)
	t.Cleanup(func() {
		if err := app.Shutdown(); err != nil {
			t.Errorf("shutdown subscription domain app: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM subscription_domains`); err != nil {
			t.Errorf("clear subscription domain fixture: %v", err)
		}
	})
	return subscriptionDomainFixture{t: t, pool: pool, app: app}
}

func (f subscriptionDomainFixture) request(method, path, body string) (int, []byte) {
	f.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	resp, err := f.app.Test(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			f.t.Errorf("close response: %v", err)
		}
	}()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, data
}
