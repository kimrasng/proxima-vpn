package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

func TestHWIDValidation(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{"Abcdefghij", true}, {"abcdefghij", true}, {"ABCDEFGHIJ", true},
		{"abcde=123-", true}, {strings.Repeat("x", 64), true},
		{"", false}, {"short", false}, {strings.Repeat("x", 65), false}, {"abcdefghij_", false}, {"abcdefghij ", false},
	} {
		if validHWID.MatchString(test.value) != test.valid {
			t.Errorf("validation mismatch for %q", test.value)
		}
	}
}

func TestHWIDSubscriptionRegistration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for isolated DB-backed HWID tests")
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
	defer pool.Exec(ctx, `DELETE FROM node_groups WHERE id = $1`, group)
	if _, err := pool.Exec(ctx, `INSERT INTO plans (id, name, duration_days, max_devices, node_group_id) VALUES ($1, 'HWID test', 30, 1, $2)`, plan, group); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM plans WHERE id = $1`, plan)
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, sub_token, plan_id, status) VALUES ($1, $2, '', $3, $4, 'active')`, user, user+"@example.test", token, plan); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user)
	app := fiber.New()
	handler := NewSubscriptionHandler(pool, 3600)
	app.Get("/sub/:sub_token/:device_id", handler.GetSubscription)
	app.Get("/sub/:sub_token", handler.GetHWIDSubscription)
	app.Delete("/devices/:id", func(c *fiber.Ctx) error {
		c.Locals("user_id", user)
		return NewUserDeviceHandler(pool).Delete(c)
	})
	defer app.Shutdown()
	request := func(hwid, path string) int {
		t.Helper()
		req := httptest.NewRequest("GET", "/sub/"+token+path, nil)
		if hwid != "" {
			req.Header.Set("x-hwid", hwid)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Error(err)
			return 0
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE user_id = $1 AND hwid_fingerprint IS NOT NULL`, user).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if status := request("", ""); status != 400 {
		t.Fatalf("missing HWID status %d", status)
	}
	if status := request("invalid", ""); status != 400 {
		t.Fatalf("invalid HWID status %d", status)
	}
	if count() != 0 {
		t.Fatal("invalid HWID registered device")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status := request("abcdefghij", ""); status != 200 {
				t.Errorf("concurrent same HWID status %d", status)
			}
		}()
	}
	wg.Wait()
	if count() != 1 {
		t.Fatalf("same HWID created %d devices", count())
	}
	var firstID, firstUUID string
	if err := pool.QueryRow(ctx, `SELECT id, xray_uuid FROM devices WHERE user_id=$1 AND hwid_fingerprint IS NOT NULL`, user).Scan(&firstID, &firstUUID); err != nil {
		t.Fatal(err)
	}
	if status := request("abcdefghij", ""); status != 200 {
		t.Errorf("refresh status %d", status)
	}
	var currentUUID string
	if err := pool.QueryRow(ctx, `SELECT xray_uuid FROM devices WHERE id=$1`, firstID).Scan(&currentUUID); err != nil || currentUUID != firstUUID {
		t.Fatalf("UUID changed: %v", err)
	}
	if status := request("", "/"+firstID); status != 200 {
		t.Errorf("legacy status %d", status)
	}
	if status := request("ABCDEFGHIJ", ""); status != 200 {
		t.Errorf("different case status %d", status)
	}
	if count() != 2 {
		t.Fatalf("case identity count %d", count())
	}
	// Settings override is account-independent and does not alter max_devices.
	var prior string
	if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key='hwid_registration_cap'`).Scan(&prior); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE settings SET value='2' WHERE key='hwid_registration_cap'`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE settings SET value=$1 WHERE key='hwid_registration_cap'`, prior)
	if status := request("0123456789", ""); status != 409 {
		t.Errorf("cap status %d", status)
	}
	if count() != 2 {
		t.Fatal("cap allowed registration")
	}
	// Distinct simultaneous registrations serialize on the account row.
	if _, err := pool.Exec(ctx, `DELETE FROM devices WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	var statuses [3]int
	for i, hwid := range []string{"0123456789", "abcdefghij", "ABCDEFGHIJ"} {
		wg.Add(1)
		go func(i int, hwid string) { defer wg.Done(); statuses[i] = request(hwid, "") }(i, hwid)
	}
	wg.Wait()
	if count() != 2 {
		t.Fatalf("concurrent cap count %d; statuses %v", count(), statuses)
	}
	failures := 0
	for _, s := range statuses {
		if s == 409 {
			failures++
		} else if s != 200 {
			t.Errorf("concurrent status %d", s)
		}
	}
	if failures != 1 {
		t.Errorf("expected one rejection: %v", statuses)
	}
	// Eligibility is checked before registration, including quota and expiry.
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_expires_at=NOW() - INTERVAL '1 hour' WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if status := request("newhwid-123", ""); status != 403 {
		t.Errorf("expired account status %d", status)
	}
	if count() != 2 {
		t.Fatal("expired account registered a device")
	}
	// Revocation retains the HWID identity but prevents legacy and HWID
	// subscription reuse, as well as creating a replacement for the same HWID.
	var revokedID string
	if err := pool.QueryRow(ctx, `SELECT id FROM devices WHERE user_id=$1 AND hwid_fingerprint IS NOT NULL AND id<>$2 LIMIT 1`, user, firstID).Scan(&revokedID); err != nil {
		t.Fatal(err)
	}
	revokeReq := httptest.NewRequest("DELETE", "/devices/"+revokedID, nil)
	revokeResp, err := app.Test(revokeReq)
	if err != nil {
		t.Fatal(err)
	}
	revokeResp.Body.Close()
	if revokeResp.StatusCode != 204 {
		t.Fatalf("revoke status %d", revokeResp.StatusCode)
	}
	if status := request("", "/"+revokedID); status != 404 {
		t.Errorf("revoked legacy status %d", status)
	}
	var retiredCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE id=$1 AND retired_at IS NOT NULL`, revokedID).Scan(&retiredCount); err != nil || retiredCount != 1 {
		t.Fatalf("missing tombstone: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_expires_at=NULL WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	// One of the concurrent identities was retired; it cannot be reissued.
	var revokedFingerprint string
	if err := pool.QueryRow(ctx, `SELECT hwid_fingerprint FROM devices WHERE id=$1`, revokedID).Scan(&revokedFingerprint); err != nil {
		t.Fatal(err)
	}
	var pepper string
	if err := pool.QueryRow(ctx, `SELECT pepper FROM hwid_secrets WHERE id=true`).Scan(&pepper); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{"0123456789", "abcdefghij", "ABCDEFGHIJ"} {
		mac := hmac.New(sha256.New, []byte(pepper))
		mac.Write([]byte(candidate))
		if hex.EncodeToString(mac.Sum(nil)) == revokedFingerprint && request(candidate, "") != 403 {
			t.Error("retired HWID was reissued")
		}
	}
	// Token rotation invalidates both subscription URLs without creating rows.
	if _, err := pool.Exec(ctx, `UPDATE users SET sub_token=$1 WHERE id=$2`, crypto.NewUUID(), user); err != nil {
		t.Fatal(err)
	}
	if status := request("abcdefghij", ""); status != 404 {
		t.Errorf("rotated token status %d", status)
	}
	if status := request("", "/"+firstID); status != 404 {
		t.Errorf("rotated legacy status %d", status)
	}
}
