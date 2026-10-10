package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

const bandwidthHandlerNode = "c718ef90-7d20-4c94-b43b-58c7dce5382e"
const bandwidthHandlerDevice = "3a2ecdb6-6283-46c0-a971-e9adeea2a27e"

func TestNodeBandwidthPermitRejectsMissingOrSpoofedNodeIdentity(t *testing.T) {
	for _, identity := range []string{"", "12465e85-b31c-4967-8a1b-8b121268d4cf"} {
		t.Run(identity, func(t *testing.T) {
			app := fiber.New()
			app.Post("/nodes/:id/bandwidth/permit", func(c *fiber.Ctx) error {
				if identity != "" {
					c.Locals("node_id", identity)
				}
				return (&NodeAgentHandler{}).BandwidthPermit(c)
			})
			req := httptest.NewRequest(fiber.MethodPost, "/nodes/"+bandwidthHandlerNode+"/bandwidth/permit", strings.NewReader(`{"device_uuid":"`+bandwidthHandlerDevice+`","direction":"upload","bytes":1}`))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != fiber.StatusUnauthorized {
				t.Errorf("status = %d, want 401", response.StatusCode)
			}
		})
	}
}

func TestNodeBandwidthPermitValidationAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{"malformed JSON", `{`, fiber.StatusBadRequest},
		{"missing device", `{"direction":"upload","bytes":1}`, fiber.StatusBadRequest},
		{"invalid UUID", `{"device_uuid":"unknown","direction":"upload","bytes":1}`, fiber.StatusBadRequest},
		{"missing direction", `{"device_uuid":"` + bandwidthHandlerDevice + `","bytes":1}`, fiber.StatusBadRequest},
		{"invalid direction", `{"device_uuid":"` + bandwidthHandlerDevice + `","direction":"both","bytes":1}`, fiber.StatusBadRequest},
		{"zero bytes", `{"device_uuid":"` + bandwidthHandlerDevice + `","direction":"upload","bytes":0}`, fiber.StatusBadRequest},
		{"negative bytes", `{"device_uuid":"` + bandwidthHandlerDevice + `","direction":"upload","bytes":-1}`, fiber.StatusBadRequest},
		{"oversized request", `{"device_uuid":"` + bandwidthHandlerDevice + `","direction":"upload","bytes":65536}`, fiber.StatusBadRequest},
		{"fractional bytes", `{"device_uuid":"` + bandwidthHandlerDevice + `","direction":"upload","bytes":1.5}`, fiber.StatusBadRequest},
		{"valid request with unavailable database", `{"device_uuid":"` + bandwidthHandlerDevice + `","direction":"upload","bytes":65535}`, fiber.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Post("/nodes/:id/bandwidth/permit", func(c *fiber.Ctx) error {
				c.Locals("node_id", bandwidthHandlerNode)
				return (&NodeAgentHandler{}).BandwidthPermit(c)
			})
			req := httptest.NewRequest(fiber.MethodPost, "/nodes/"+bandwidthHandlerNode+"/bandwidth/permit", strings.NewReader(tc.body))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			response, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", response.StatusCode, tc.status, body)
			}
			var result map[string]any
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if result["allowed"] == true {
				t.Errorf("invalid or failed request granted bytes: %s", body)
			}
		})
	}
}
