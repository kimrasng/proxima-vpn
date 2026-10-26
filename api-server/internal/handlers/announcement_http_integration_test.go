package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
)

func TestAnnouncementLocalizedLifecycle(t *testing.T) {
	// Given an isolated, migrated database supplied explicitly for this test.
	dsn := os.Getenv("ANNOUNCEMENT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ANNOUNCEMENT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, language)
		VALUES (gen_random_uuid()::text || '@announcement.test', 'test', 'ko') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID); err != nil {
			t.Error(err)
		}
	})
	admin := NewAdminAnnouncementHandler(pool)
	user := NewUserPortalHandler(pool, nil)
	app := fiber.New()
	app.Post("/announcements", admin.Create)
	app.Get("/announcements", admin.List)
	app.Put("/announcements/:id", admin.Update)
	app.Delete("/announcements/:id", admin.Delete)
	app.Get("/user/announcements", func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return user.ListLocalizedAnnouncements(c)
	})
	request := func(method, path, body string, wantStatus int, result interface{}) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != wantStatus {
			var message interface{}
			if err := json.NewDecoder(res.Body).Decode(&message); err != nil {
				t.Fatal(err)
			}
			t.Fatalf("%s %s: status %d, body %v", method, path, res.StatusCode, message)
		}
		if result != nil {
			if err := json.NewDecoder(res.Body).Decode(result); err != nil {
				t.Fatal(err)
			}
		}
	}

	// When an announcement is created, translated, reformatted and deleted.
	// Then each HTTP response reflects persisted, sanitized, localized content.
	var created announcementItem
	request("POST", "/announcements", `{"title":"Base","content":"**Base**","format":"markdown",
		"translations":[{"language":"ko","title":"Translated","content":"**Translated**<script>alert(1)</script>"}]}`, 201, &created)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM announcements WHERE id=$1`, created.ID); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(created.RenderedHTML, "<strong>Base</strong>") || len(created.Translations) != 1 {
		t.Fatalf("unexpected created announcement: %+v", created)
	}
	type localizedItem struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		HTML    string `json:"content_html"`
	}
	var localized []localizedItem
	request("GET", "/user/announcements", "", 200, &localized)
	if len(localized) != 1 || localized[0].Title != "Translated" ||
		!strings.Contains(localized[0].HTML, "<strong>Translated</strong>") || strings.Contains(localized[0].HTML, "<script") {
		t.Fatalf("profile language or sanitized HTML missing: %+v", localized)
	}
	request("GET", "/user/announcements?lang=en", "", 200, &localized)
	if localized[0].Title != "Base" || localized[0].Content != "**Base**" {
		t.Fatalf("base fallback missing: %+v", localized)
	}
	var updated announcementItem
	request("PUT", "/announcements/"+created.ID, `{"format":"html"}`, 200, &updated)
	request("GET", "/user/announcements?lang=ko", "", 200, &localized)
	if strings.Contains(updated.RenderedHTML, "<strong>") || strings.Contains(localized[0].HTML, "<strong>") {
		t.Fatal("format-only update did not rerender base and translations")
	}
	request("PUT", "/announcements/"+created.ID, `{"translations":[],"image_url":null}`, 200, &updated)
	request("GET", "/user/announcements?lang=ko", "", 200, &localized)
	if localized[0].Title != "Base" || len(updated.Translations) != 0 {
		t.Fatal("removed translation did not fall back to base")
	}
	request("PUT", "/announcements/"+created.ID, `{"translations":[{"language":"zh","title":"News","content":"<h2>News</h2>"}]}`, 200, &updated)
	var listed []announcementItem
	request("GET", "/announcements", "", 200, &listed)
	if len(listed) != 1 || len(listed[0].Translations) != 1 {
		t.Fatal("admin list did not return translations")
	}
	request("DELETE", "/announcements/"+created.ID, "", 204, nil)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM announcement_translations WHERE announcement_id=$1`, created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("translation rows survived announcement deletion")
	}
}
