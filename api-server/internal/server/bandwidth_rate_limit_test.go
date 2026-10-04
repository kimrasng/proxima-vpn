package server

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"net/http/httptest"
	"testing"
)

func TestBandwidthPermitLimiterExemptionRequiresAuthenticatedExactRoute(t *testing.T) {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		if c.Get("Authenticated") == "true" {
			c.Locals("bandwidth_authenticated_node", "00000000-0000-0000-0000-000000000001")
		}
		return c.JSON(isBandwidthPermitRequest(c))
	})
	for _, tc := range []struct {
		method, path, authenticated string
		want                        bool
	}{
		{"POST", "/api/v1/nodes/00000000-0000-0000-0000-000000000001/bandwidth/permit", "true", true},
		{"GET", "/api/v1/nodes/00000000-0000-0000-0000-000000000001/bandwidth/permit", "true", false},
		{"POST", "/api/v1/nodes/00000000-0000-0000-0000-000000000001/config", "true", false},
		{"POST", "/api/v1/nodes/no/bandwidth/permit", "true", false},
		{"POST", "/api/v1/nodes/00000000-0000-0000-0000-000000000001/bandwidth/permit", "false", false},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Node-Key", "arbitrary")
		req.Header.Set("Authenticated", tc.authenticated)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var got bool
		if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got != tc.want {
			t.Errorf("%s %s got%v", tc.method, tc.path, got)
		}
	}
}

func TestInvalidBandwidthKeysAreThrottledBeforeDatabaseAuthentication(t *testing.T) {
	app := fiber.New()
	guard, auth := bandwidthAuthentication(nil)
	app.Use(guard, auth)
	app.Post("/api/v1/nodes/:id/bandwidth/permit", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	for i := 0; i < 101; i++ {
		req := httptest.NewRequest("POST", "/api/v1/nodes/00000000-0000-0000-0000-000000000001/bandwidth/permit", nil)
		req.Header.Set("X-Node-Key", "arbitrary")
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if i < 100 && res.StatusCode != 401 {
			t.Fatalf("request%d status%d", i, res.StatusCode)
		}
		if i == 100 && res.StatusCode != 429 {
			t.Fatalf("invalid keys bypassed abuseguard status%d", res.StatusCode)
		}
	}
}
