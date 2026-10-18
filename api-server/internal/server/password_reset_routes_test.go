package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// resetRouteStatuses fires n requests at one route and returns each status.
// The app has no database and no SMTP configuration, so a well-formed
// forgot-password request answers 503 and a weak-password reset answers 400
// without touching storage; only the rate limiter can produce a 429.
func resetRouteStatuses(t *testing.T, path, body string, n int) []int {
	t.Helper()
	t.Setenv("SWAGGER_ENABLED", "false")
	app := NewServer(&config.Config{}, nil, nil, nil, nil).App()
	t.Cleanup(func() { _ = app.Shutdown() })

	statuses := make([]int, 0, n)
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(fiber.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		_ = res.Body.Close()
		statuses = append(statuses, res.StatusCode)
	}
	return statuses
}

func TestForgotPasswordRoute_IsLimitedToFiveRequestsPerClient(t *testing.T) {
	got := resetRouteStatuses(t, "/api/v1/auth/forgot-password", `{"email":"someone@example.com"}`, 7)

	for i, status := range got {
		want := fiber.StatusServiceUnavailable
		if i >= 5 {
			want = fiber.StatusTooManyRequests
		}
		if status != want {
			t.Fatalf("request %d: status %d, want %d (all: %v)", i+1, status, want, got)
		}
	}
}

func TestResetPasswordRoute_IsLimitedToTenRequestsPerClient(t *testing.T) {
	got := resetRouteStatuses(t, "/api/v1/auth/reset-password", `{"token":"some-token","password":"short"}`, 12)

	for i, status := range got {
		want := fiber.StatusBadRequest
		if i >= 10 {
			want = fiber.StatusTooManyRequests
		}
		if status != want {
			t.Fatalf("request %d: status %d, want %d (all: %v)", i+1, status, want, got)
		}
	}
}
