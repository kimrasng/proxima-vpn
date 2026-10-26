package server

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

func TestSupportEndpointsRequireTheirOwnLogin(t *testing.T) {
	t.Setenv("SWAGGER_ENABLED", "false")
	s := NewServer(&config.Config{}, nil, nil, nil, nil)
	defer func() { _ = s.App().Shutdown() }()
	for _, tc := range []struct{ method, path string }{
		{fiber.MethodGet, "/api/v1/user/support/faq"},
		{fiber.MethodGet, "/api/v1/user/support/tickets"},
		{fiber.MethodPost, "/api/v1/user/support/tickets"},
		{fiber.MethodGet, "/api/v1/user/support/tickets/00000000-0000-4000-8000-000000000000"},
		{fiber.MethodPost, "/api/v1/user/support/tickets/00000000-0000-4000-8000-000000000000/messages"},
		{fiber.MethodGet, "/api/v1/admin/support/tickets"},
		{fiber.MethodGet, "/api/v1/admin/support/summary"},
		{fiber.MethodPost, "/api/v1/admin/support/tickets/00000000-0000-4000-8000-000000000000/messages"},
		{fiber.MethodDelete, "/api/v1/admin/support/tickets/00000000-0000-4000-8000-000000000000"},
		{fiber.MethodPost, "/api/v1/admin/support/categories"},
		{fiber.MethodPost, "/api/v1/admin/support/faqs"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		res, err := s.App().Test(req, -1)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		_ = res.Body.Close()
		if res.StatusCode != fiber.StatusUnauthorized {
			t.Errorf("%s %s without a token = %d, want 401", tc.method, tc.path, res.StatusCode)
		}
	}
}
