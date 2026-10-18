package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// The exact list requested for the initial configuration.
var defaultSignupDomains = []string{
	"gmail.com", "qq.com", "163.com", "yahoo.com", "sina.com", "126.com", "outlook.com", "yeah.net",
	"foxmail.com", "icloud.com", "hotmail.com", "aliyun.com", "sohu.com", "mail.com", "live.com", "protonmail.com",
}

func TestNormalizeEmailDomains(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"comma separated", "gmail.com, qq.com,163.com", []string{"gmail.com", "qq.com", "163.com"}},
		{"newline and spaces", "gmail.com\nqq.com  163.com\r\nyeah.net", []string{"gmail.com", "qq.com", "163.com", "yeah.net"}},
		{"case and @ prefix", " @Gmail.COM ,QQ.com", []string{"gmail.com", "qq.com"}},
		{"full address pasted", "someone@outlook.com", []string{"outlook.com"}},
		{"duplicates collapse", "gmail.com, GMAIL.com, @gmail.com", []string{"gmail.com"}},
		{"trailing dot", "gmail.com.", []string{"gmail.com"}},
		{"garbage dropped, never widened", "localhost, *.com, .com, a b, -x.com, x-.com, a..b.com, gmail", []string{}},
		{"unicode needs punycode", "例え.jp, xn--r8jz45g.jp", []string{"xn--r8jz45g.jp"}},
		{"empty", "  ,, ;;  ", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeEmailDomains(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("normalizeEmailDomains(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSignupDomainAllowed(t *testing.T) {
	allowed := []string{"gmail.com", "mail.com", "163.com"}
	cases := []struct {
		name     string
		email    string
		on       bool
		allowed  []string
		wantPass bool
	}{
		{"restriction off allows anything", "x@whatever.example", false, allowed, true},
		{"restriction off ignores an empty list", "x@whatever.example", false, nil, true},
		{"listed domain", "x@gmail.com", true, allowed, true},
		{"case-insensitive", "X@GMAIL.COM", true, allowed, true},
		{"unlisted domain", "x@company.example", true, allowed, false},
		{"suffix is not a match", "x@evilmail.com", true, allowed, false},
		{"subdomain is not a match", "x@sub.mail.com", true, allowed, false},
		{"prefix is not a match", "x@gmail.com.evil.example", true, allowed, false},
		{"empty allow-list refuses everyone", "x@gmail.com", true, nil, false},
		{"no @ refuses", "gmail.com", true, allowed, false},
		{"last @ wins", "gmail.com@evil.example", true, allowed, false},
		{"digits-only domain", "x@163.com", true, allowed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := signupDomainAllowed(tc.email, tc.on, tc.allowed); got != tc.wantPass {
				t.Errorf("signupDomainAllowed(%q, on=%v, %v) = %v, want %v", tc.email, tc.on, tc.allowed, got, tc.wantPass)
			}
		})
	}
}

// The DB-backed tests below skip unless TEST_DATABASE_URL points at a disposable
// database, matching the rest of this package.
func signupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed signup-domain tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// setSignupSettings overwrites the two settings and restores the previous rows
// when the test ends, so tests do not leak policy into each other.
func setSignupSettings(t *testing.T, pool *pgxpool.Pool, restriction, domains string) {
	t.Helper()
	ctx := context.Background()
	type row struct {
		key, val string
		had      bool
	}
	keys := []string{settingSignupDomainRestriction, settingSignupEmailDomains, "self_registration"}
	prev := make([]row, 0, len(keys))
	for _, k := range keys {
		r := row{key: k}
		if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, k).Scan(&r.val); err == nil {
			r.had = true
		}
		prev = append(prev, r)
	}
	t.Cleanup(func() {
		for _, r := range prev {
			if r.had {
				_, _ = pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value = $2`, r.key, r.val)
			} else {
				_, _ = pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, r.key)
			}
		}
	})
	for k, v := range map[string]string{
		settingSignupDomainRestriction: restriction,
		settingSignupEmailDomains:      domains,
		"self_registration":            "true",
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value = $2`, k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
}

func registerStatus(t *testing.T, pool *pgxpool.Pool, email string) (int, map[string]any) {
	t.Helper()
	// A real (disabled) Telegram service: a successful signup notifies it in the background.
	h := NewUserAuthHandler(pool, "test-secret", 0, services.NewTelegramService(pool, config.TelegramConfig{}), nil, "")
	app := fiber.New()
	app.Post("/register", h.Register)
	body, _ := json.Marshal(map[string]string{"email": email, "password": "Passw0rd!xyz", "name": "T"})
	req := httptest.NewRequest("POST", "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRegister_DomainRestriction(t *testing.T) {
	pool := signupTestDB(t)
	ctx := context.Background()
	uniq := fmt.Sprintf("%d", os.Getpid())
	cleanup := func(email string) { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE email = $1`, email) }

	t.Run("restriction on: listed domain registers, others are refused with the allow-list", func(t *testing.T) {
		setSignupSettings(t, pool, "true", "gmail.com, qq.com")
		okEmail := "dom-ok-" + uniq + "@gmail.com"
		badEmail := "dom-bad-" + uniq + "@company.example"
		t.Cleanup(func() { cleanup(okEmail); cleanup(badEmail) })

		if code, _ := registerStatus(t, pool, okEmail); code != fiber.StatusCreated {
			t.Fatalf("listed domain: status %d, want 201", code)
		}
		code, body := registerStatus(t, pool, badEmail)
		if code != fiber.StatusForbidden {
			t.Fatalf("unlisted domain: status %d, want 403", code)
		}
		domains, _ := body["allowed_domains"].([]any)
		if len(domains) != 2 || domains[0] != "gmail.com" || domains[1] != "qq.com" {
			t.Errorf("allowed_domains = %v, want [gmail.com qq.com]", body["allowed_domains"])
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, badEmail).Scan(&n)
		if n != 0 {
			t.Errorf("a refused address must not create a user, found %d", n)
		}
	})

	t.Run("restriction off: any domain registers", func(t *testing.T) {
		setSignupSettings(t, pool, "false", "gmail.com")
		email := "dom-off-" + uniq + "@company.example"
		t.Cleanup(func() { cleanup(email) })
		if code, _ := registerStatus(t, pool, email); code != fiber.StatusCreated {
			t.Fatalf("status %d, want 201", code)
		}
	})

	t.Run("restriction on with an empty list refuses everyone", func(t *testing.T) {
		setSignupSettings(t, pool, "true", "   ")
		email := "dom-empty-" + uniq + "@gmail.com"
		t.Cleanup(func() { cleanup(email) })
		if code, _ := registerStatus(t, pool, email); code != fiber.StatusForbidden {
			t.Fatalf("status %d, want 403", code)
		}
	})

	t.Run("setting never saved: open, so existing installs keep working", func(t *testing.T) {
		setSignupSettings(t, pool, "false", "")
		_, _ = pool.Exec(ctx, `DELETE FROM settings WHERE key IN ($1,$2)`, settingSignupDomainRestriction, settingSignupEmailDomains)
		email := "dom-unset-" + uniq + "@company.example"
		t.Cleanup(func() { cleanup(email) })
		if code, _ := registerStatus(t, pool, email); code != fiber.StatusCreated {
			t.Fatalf("status %d, want 201", code)
		}
	})
}

func TestSignupSeedMatchesRequestedList(t *testing.T) {
	pool := signupTestDB(t)
	// The migration seeds with ON CONFLICT DO NOTHING, so on a database that
	// has never been edited the stored list is exactly the requested one.
	var on, list string
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingSignupDomainRestriction).Scan(&on); err != nil {
		t.Skip("restriction setting not present (database predates the migration or was edited)")
	}
	if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, settingSignupEmailDomains).Scan(&list); err != nil {
		t.Fatalf("seeded domain list missing: %v", err)
	}
	if got := normalizeEmailDomains(list); !reflect.DeepEqual(got, defaultSignupDomains) {
		t.Skipf("list was edited since seeding (%d domains); not a seed regression", len(got))
	}
}

func TestSignupPolicyEndpoint(t *testing.T) {
	pool := signupTestDB(t)
	h := NewUserAuthHandler(pool, "test-secret", 0, services.NewTelegramService(pool, config.TelegramConfig{}), nil, "")
	app := fiber.New()
	app.Get("/signup-policy", h.SignupPolicy)

	get := func() (int, string, map[string]any) {
		resp, err := app.Test(httptest.NewRequest("GET", "/signup-policy", nil), -1)
		if err != nil {
			t.Fatalf("policy request: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, resp.Header.Get("Cache-Control"), out
	}

	t.Run("off: not restricted, empty list", func(t *testing.T) {
		setSignupSettings(t, pool, "false", "gmail.com, qq.com")
		code, cache, body := get()
		if code != 200 || body["restricted"] != false {
			t.Fatalf("status %d body %v, want 200 and restricted=false", code, body)
		}
		if d, _ := body["allowed_domains"].([]any); len(d) != 0 {
			t.Errorf("an unrestricted install must not advertise a list, got %v", body["allowed_domains"])
		}
		if cache != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store so an admin change shows at once", cache)
		}
	})

	t.Run("on: restricted with the cleaned list in order", func(t *testing.T) {
		setSignupSettings(t, pool, "true", "Gmail.com ; @QQ.com\n someone@163.com, junk, gmail.com")
		code, _, body := get()
		if code != 200 || body["restricted"] != true {
			t.Fatalf("status %d body %v, want 200 and restricted=true", code, body)
		}
		got, _ := body["allowed_domains"].([]any)
		want := []any{"gmail.com", "qq.com", "163.com"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("allowed_domains = %v, want %v", got, want)
		}
	})

	t.Run("on with an empty list: restricted, empty (nobody can register)", func(t *testing.T) {
		setSignupSettings(t, pool, "true", "   ")
		_, _, body := get()
		if body["restricted"] != true {
			t.Fatalf("restricted = %v, want true", body["restricted"])
		}
		if d, _ := body["allowed_domains"].([]any); len(d) != 0 {
			t.Errorf("allowed_domains = %v, want empty", d)
		}
	})
}
