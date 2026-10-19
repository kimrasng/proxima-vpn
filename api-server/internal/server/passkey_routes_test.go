package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

func TestPasskeyLoginRoutesAreLimited(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	app := NewServer(&config.Config{}, nil, nil, nil, nil).App()
	t.Cleanup(func() { _ = app.Shutdown() })

	for i := 0; i < 12; i++ {
		req := httptest.NewRequest(fiber.MethodPost, "/api/v1/auth/passkey/login/begin", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		want := fiber.StatusServiceUnavailable
		if i >= 10 {
			want = fiber.StatusTooManyRequests
		}
		if res.StatusCode != want {
			t.Errorf("request %d: status %d, want %d", i+1, res.StatusCode, want)
		}
		_ = res.Body.Close()
	}
}
