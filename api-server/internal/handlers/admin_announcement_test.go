package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAnnouncementPreviewSanitizesHTML(t *testing.T) {
	// Given a real preview route, without a database dependency.
	app := fiber.New()
	app.Post("/preview", NewAdminAnnouncementHandler(nil).Preview)
	body := `{"format":"html","content":"<h2>News</h2><script>alert(1)</script><img src=\"https://example.com/a.png\" onerror=\"alert(1)\">"}`
	req := httptest.NewRequest("POST", "/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// When an administrator previews HTML.
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	// Then structure survives, but executable content does not.
	var result struct {
		HTML string `json:"html"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.Contains(result.HTML, "<h2>News</h2>") ||
		strings.Contains(result.HTML, "<script") || strings.Contains(result.HTML, "onerror") {
		t.Fatalf("unexpected response: %d %s", res.StatusCode, result.HTML)
	}
}

func TestAnnouncementTranslationsRenderAndValidate(t *testing.T) {
	for _, format := range []string{"markdown", "html"} {
		t.Run(format, func(t *testing.T) {
			// Given a translation with HTML that must be sanitized in either format.
			translations := []translationItem{{Language: "ko", Title: "News", Content: "<strong>Safe</strong><script>alert(1)</script>"}}

			// When base and translated content pass through the announcement renderer.
			_, err := renderAnnouncement(format, "Base", translations)

			// Then the translated HTML is rendered by the same sanitizer.
			if err != nil {
				t.Fatal(err)
			}
			html := translations[0].RenderedHTML
			if !strings.Contains(html, "<strong>Safe</strong>") || strings.Contains(html, "<script") {
				t.Fatalf("unsafe or missing translated HTML: %s", html)
			}
		})
	}
	for _, tc := range []struct {
		name         string
		translations []translationItem
	}{
		{"unsupported language", []translationItem{{Language: "fr", Title: "Title", Content: "Body"}}},
		{"duplicate language", []translationItem{{Language: "en", Title: "Title", Content: "Body"}, {Language: "en", Title: "Other", Content: "Other"}}},
		{"partial translation", []translationItem{{Language: "zh", Title: "Title"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given an invalid translation set.
			// When it is rendered for persistence.
			_, err := renderAnnouncement("markdown", "Base", tc.translations)
			// Then it is rejected before the transaction can write it.
			if err == nil {
				t.Fatal("invalid translations accepted")
			}
		})
	}
}
