package handlers

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

func TestAccountSubscriptionSharesOneUUIDWithoutClientIdentity(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for DB-backed account subscription tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	group, plan, user, token := crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID(), crypto.NewUUID()
	if _, err := pool.Exec(ctx, `INSERT INTO node_groups (id, name) VALUES ($1, $2)`, group, group); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, group) }()
	if _, err := pool.Exec(ctx, `INSERT INTO plans (id, name, duration_days, max_devices, node_group_id) VALUES ($1, 'account sub test', 30, 1, $2)`, plan, group); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, plan) }()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, sub_token, plan_id, status) VALUES ($1, $2, '', $3, $4, 'active')`, user, user+"@example.test", token, plan); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user) }()

	app := fiber.New()
	handler := NewSubscriptionHandler(pool, 3600)
	app.Get("/sub/:sub_token/:device_id", handler.GetSubscription)
	app.Get("/sub/:sub_token", handler.GetAccountSubscription)
	defer func() { _ = app.Shutdown() }()
	type result struct {
		status      int
		contentType string
		userinfo    string
	}
	fetch := func(path string, headers map[string]string) result {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Error(err)
			return result{}
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return result{resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Subscription-Userinfo")}
	}
	request := func(path string, headers map[string]string) int {
		t.Helper()
		return fetch(path, headers).status
	}
	devices := func() []string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT xray_uuid FROM devices WHERE user_id = $1 AND retired_at IS NULL`, user)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}

	// Concurrent first fetches from different apps (no x-hwid, varied headers)
	// must not fail and must converge on exactly one account UUID.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			headers := map[string]string{"User-Agent": "v2rayNG/1.9.16"}
			if i%2 == 0 {
				headers["x-hwid"] = crypto.NewUUID() // ignored by design
			}
			if status := request("/sub/"+token, headers); status != 200 {
				t.Errorf("first fetch status %d", status)
			}
		}(i)
	}
	wg.Wait()
	first := devices()
	if len(first) != 1 {
		t.Fatalf("account devices after concurrent first fetch: %d", len(first))
	}
	// Later refreshes, with or without headers, reuse the same UUID.
	for _, headers := range []map[string]string{nil, {"x-hwid": "abcdefghij"}, {"User-Agent": "Happ/1.63.1"}} {
		if status := request("/sub/"+token, headers); status != 200 {
			t.Errorf("refresh status %d", status)
		}
	}
	if after := devices(); len(after) != 1 || after[0] != first[0] {
		t.Fatalf("account UUID changed: %v -> %v", first, after)
	}
	// The same URL answers each client in its own format. This fixture has no
	// ready node, so profile formats that cannot be empty ask the client to
	// retry; share links stay a valid (empty) list.
	if r := fetch("/sub/"+token, map[string]string{"User-Agent": "v2rayN/6.42"}); r.status != 200 || !strings.HasPrefix(r.contentType, "text/plain") || !strings.HasPrefix(r.userinfo, "upload=0; download=") {
		t.Errorf("v2rayN fetch: %+v", r)
	}
	for _, ua := range []string{"clash-verge/v2.0.3", "mihomo/1.18.0", "SFA/1.10.0", "HiddifyNext/2.5.7"} {
		if status := request("/sub/"+token, map[string]string{"User-Agent": ua}); status != 503 {
			t.Errorf("%s without ready nodes status %d, want 503", ua, status)
		}
	}
	if r := fetch("/sub/"+token, map[string]string{"User-Agent": "Mozilla/5.0", "Accept": "text/html"}); r.status != 200 || !strings.HasPrefix(r.contentType, "text/html") {
		t.Errorf("browser fetch: %+v", r)
	}
	// Path overrides serve the account URL for unrecognized apps.
	if status := request("/sub/"+token+"/clash-meta", map[string]string{"User-Agent": "unknown-app"}); status != 503 {
		t.Errorf("clash-meta override status %d, want 503", status)
	}
	if r := fetch("/sub/"+token+"/v2ray", map[string]string{"User-Agent": "clash-verge/v2.0.3"}); r.status != 200 || !strings.HasPrefix(r.contentType, "text/plain") {
		t.Errorf("v2ray override: %+v", r)
	}
	if after := devices(); len(after) != 1 || after[0] != first[0] {
		t.Fatalf("client formats changed the account UUID: %v -> %v", first, after)
	}
	// A legacy device link still resolves that device.
	var legacyID string
	if err := pool.QueryRow(ctx, `SELECT id FROM devices WHERE user_id = $1`, user).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if status := request("/sub/"+token+"/"+legacyID, nil); status != 200 {
		t.Errorf("legacy device link status %d", status)
	}
	if status := request("/sub/"+crypto.NewUUID(), nil); status != 404 {
		t.Errorf("unknown token status %d", status)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_expires_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, user); err != nil {
		t.Fatal(err)
	}
	if status := request("/sub/"+token, nil); status != 403 {
		t.Errorf("expired account status %d", status)
	}
}
