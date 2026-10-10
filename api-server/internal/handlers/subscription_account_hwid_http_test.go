package handlers

import (
	"context"
	"encoding/base64"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// TestAccountSubscriptionHWIDSlots covers the x-hwid slot path of the account
// URL: a valid x-hwid selects, or registers, its own device UUID up to
// hwid_registration_cap, while an invalid one falls back to the shared account
// device exactly like a request without x-hwid. The fixture has a ready relayed
// chain, so the UUID actually served is read back from the v2ray share links.
func TestAccountSubscriptionHWIDSlots(t *testing.T) {
	f := newSubscriptionHTTPFixture(t, chainTestDB(t))
	f.app.Get("/sub/:sub_token", NewSubscriptionHandler(f.pool, 3600).GetAccountSubscription)
	ctx := context.Background()
	accountPath := f.path[:strings.LastIndex(f.path, "/")]
	subToken := strings.TrimPrefix(accountPath, "/sub/")

	// A new device changes the Exit's generated config, and share links are
	// only served once the node has acknowledged the current config. Mark the
	// current config applied before any request whose served UUID is checked.
	applyConfig := func() {
		t.Helper()
		digest, err := services.NewXrayConfigService(f.pool).GenerateDigest(ctx, f.exitID)
		if err != nil {
			t.Fatalf("generate exit config digest: %v", err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE nodes SET config_hash = $2, last_seen = NOW() WHERE id = $1`, f.exitID, digest.Hash); err != nil {
			t.Fatalf("apply exit config hash: %v", err)
		}
	}
	type result struct {
		status int
		uuid   string // UUID in the first share link, if any were served
		body   string
	}
	fetch := func(hwid string) result {
		t.Helper()
		req := httptest.NewRequest(fiber.MethodGet, accountPath, nil)
		req.Header.Set(fiber.HeaderUserAgent, "v2rayNG/1.9.16")
		if hwid != "" {
			req.Header.Set("x-hwid", hwid)
		}
		resp, err := f.app.Test(req, 10_000) // concurrent requests may wait on locks
		if err != nil {
			t.Error(err)
			return result{}
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Error(err)
			return result{}
		}
		r := result{status: resp.StatusCode, body: string(body)}
		if r.status != fiber.StatusOK {
			return r
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(r.body))
		if err != nil {
			t.Errorf("not base64 share links: %v: %s", err, body)
			return r
		}
		if links := strings.Fields(string(decoded)); len(links) > 0 {
			link, err := url.Parse(links[0])
			if err != nil || link.User == nil {
				t.Errorf("unexpected share link %q (%v)", links[0], err)
				return r
			}
			r.uuid = link.User.Username()
		}
		return r
	}
	// devices returns the UUIDs of the account's live HWID slots and the number
	// of live devices without a fingerprint.
	devices := func() (slots []string, shared int) {
		t.Helper()
		rows, err := f.pool.Query(ctx, `SELECT xray_uuid, hwid_fingerprint IS NOT NULL FROM devices
 WHERE user_id = (SELECT id FROM users WHERE sub_token = $1) AND retired_at IS NULL
 ORDER BY created_at, id`, subToken)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var uuid string
			var slot bool
			if err := rows.Scan(&uuid, &slot); err != nil {
				t.Fatal(err)
			}
			if slot {
				slots = append(slots, uuid)
			} else {
				shared++
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return slots, shared
	}

	// Without x-hwid the account serves its shared (fixture) device.
	sharedFetch := fetch("")
	if sharedFetch.status != fiber.StatusOK || sharedFetch.uuid == "" {
		t.Fatalf("fetch without x-hwid: %+v", sharedFetch)
	}
	sharedUUID := sharedFetch.uuid

	// An invalid x-hwid is treated as absent: same shared UUID, no new device.
	for _, invalid := range []string{"short", "has spaces in it", strings.Repeat("x", 65)} {
		if r := fetch(invalid); r.status != fiber.StatusOK || r.uuid != sharedUUID {
			t.Errorf("invalid x-hwid %q: status %d uuid %q, want shared %q", invalid, r.status, r.uuid, sharedUUID)
		}
	}
	if slots, shared := devices(); len(slots) != 0 || shared != 1 {
		t.Fatalf("after invalid x-hwid fetches: %d slots, %d shared devices", len(slots), shared)
	}

	// Concurrent first fetches with the same valid x-hwid register exactly one
	// slot; the row lock on the user serialises the registration. A separate
	// connection holds an EXCLUSIVE lock on devices (reads still pass) until
	// every request the pool can run at once is parked inside its transaction,
	// so the requests genuinely overlap. Without the user row lock they would
	// all miss the slot lookup and race on the insert, and the losers would hit
	// the unique fingerprint index.
	hwid := crypto.NewUUID()
	blocker, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("connect lock holder: %v", err)
	}
	defer func() { _ = blocker.Close(ctx) }()
	lockTx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockTx.Rollback(ctx) }()
	if _, err := lockTx.Exec(ctx, `LOCK TABLE devices IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock devices: %v", err)
	}
	const concurrent = 8
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if r := fetch(hwid); r.status != fiber.StatusOK {
				t.Errorf("concurrent first fetch %d: status %d: %s", i, r.status, r.body)
			}
		}(i)
	}
	wantWaiting := min(concurrent, int(f.pool.Config().MaxConns))
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		// pg_locks is read live; pg_stat_activity would be a snapshot cached
		// for the duration of lockTx.
		var waiting int
		if err := lockTx.QueryRow(ctx, `SELECT count(DISTINCT pid) FROM pg_locks WHERE NOT granted`).Scan(&waiting); err != nil {
			t.Fatalf("count waiting requests: %v", err)
		}
		if waiting >= wantWaiting {
			break
		}
		if time.Now().After(deadline) {
			_ = lockTx.Rollback(ctx)
			wg.Wait()
			t.Fatalf("only %d of %d concurrent requests reached a lock wait", waiting, wantWaiting)
		}
	}
	if err := lockTx.Rollback(ctx); err != nil {
		t.Fatalf("release devices lock: %v", err)
	}
	wg.Wait()
	slots, shared := devices()
	if len(slots) != 1 || shared != 1 {
		t.Fatalf("after concurrent first fetch with one x-hwid: %d slots, %d shared devices", len(slots), shared)
	}
	slotUUID := slots[0]
	if slotUUID == sharedUUID {
		t.Fatalf("HWID slot reused the shared UUID %q", sharedUUID)
	}

	// A refresh with the same x-hwid serves that slot's UUID and adds nothing.
	applyConfig()
	if r := fetch(hwid); r.status != fiber.StatusOK || r.uuid != slotUUID {
		t.Errorf("refresh with same x-hwid: status %d uuid %q, want %q", r.status, r.uuid, slotUUID)
	}
	if slots, shared := devices(); len(slots) != 1 || shared != 1 {
		t.Fatalf("after refresh: %d slots, %d shared devices", len(slots), shared)
	}

	// Distinct valid x-hwids each register one new slot up to the cap. The
	// handler falls back to 10 when the setting is missing or unreadable.
	var cap int
	if err := f.pool.QueryRow(ctx, `SELECT value::int FROM settings WHERE key = 'hwid_registration_cap'`).Scan(&cap); err != nil {
		cap = 10
	}
	if cap < 1 {
		t.Fatalf("hwid_registration_cap = %d, need at least 1 for this test", cap)
	}
	for registered := 1; registered < cap; registered++ {
		if r := fetch(crypto.NewUUID()); r.status != fiber.StatusOK {
			t.Fatalf("registering slot %d of %d: status %d: %s", registered+1, cap, r.status, r.body)
		}
		if slots, shared := devices(); len(slots) != registered+1 || shared != 1 {
			t.Fatalf("after registering slot %d of %d: %d slots, %d shared devices", registered+1, cap, len(slots), shared)
		}
	}

	// Beyond the cap a new x-hwid is refused with 403 and registers nothing.
	if r := fetch(crypto.NewUUID()); r.status != fiber.StatusForbidden || strings.TrimSpace(r.body) != `{"error":"hwid registration cap reached"}` {
		t.Errorf("x-hwid beyond cap: status %d body %s, want 403 cap reached", r.status, r.body)
	}
	if slots, shared := devices(); len(slots) != cap || shared != 1 {
		t.Fatalf("after refused registration: %d slots, %d shared devices, want %d slots", len(slots), shared, cap)
	}

	// At the cap, registered slots and the shared device keep their UUIDs.
	applyConfig()
	if r := fetch(hwid); r.status != fiber.StatusOK || r.uuid != slotUUID {
		t.Errorf("refresh of registered x-hwid at cap: status %d uuid %q, want %q", r.status, r.uuid, slotUUID)
	}
	if r := fetch(""); r.status != fiber.StatusOK || r.uuid != sharedUUID {
		t.Errorf("fetch without x-hwid at cap: status %d uuid %q, want %q", r.status, r.uuid, sharedUUID)
	}
}
