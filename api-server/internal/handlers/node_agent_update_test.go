package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func newDownloadTestApp() *fiber.App {
	h := &NodeAgentHandler{}
	app := fiber.New()
	app.Get("/nodes/:id/update/download", h.DownloadUpdate)
	return app
}

func TestDownloadUpdateRejectsInvalidOSArch(t *testing.T) {
	app := newDownloadTestApp()

	cases := []string{
		"/nodes/n1/update/download?os=windows&arch=amd64",
		"/nodes/n1/update/download?os=linux&arch=mips",
		"/nodes/n1/update/download?os=../../etc&arch=amd64",
		"/nodes/n1/update/download",
	}
	for _, url := range cases {
		req := httptest.NewRequest(fiber.MethodGet, url, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test(%q) error: %v", url, err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Errorf("url %q: expected 400, got %d", url, resp.StatusCode)
		}
	}
}

func TestDownloadUpdateMissingBinaryReturns404(t *testing.T) {
	app := newDownloadTestApp()

	req := httptest.NewRequest(fiber.MethodGet, "/nodes/n1/update/download?os=linux&arch=arm64", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test error: %v", err)
	}
	// The binary does not exist at /app/downloads in the test environment.
	if resp.StatusCode != fiber.StatusNotFound {
		t.Errorf("expected 404 for missing binary, got %d", resp.StatusCode)
	}
}
