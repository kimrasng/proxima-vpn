package handlers

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// AdminAlertHandler mutates the lifecycle of a single alert.
type AdminAlertHandler struct {
	alerts   *services.AlertService
	activity *services.ActivityService
}

func NewAdminAlertHandler(db *pgxpool.Pool) *AdminAlertHandler {
	return &AdminAlertHandler{
		alerts:   services.NewAlertService(db),
		activity: services.NewActivityService(db),
	}
}

// patchAlertRequest carries pointers so an omitted field is distinguishable from
// an explicit false or zero, which is what lets one endpoint both set and clear.
type patchAlertRequest struct {
	Ack            *bool `json:"ack"`
	SilenceMinutes *int  `json:"silence_minutes"`
}

// silenceMaxMinutes caps a silence at one day. An indefinite silence is how a
// condition gets forgotten about entirely.
const silenceMaxMinutes = 24 * 60

// Patch acknowledges or silences an alert.
//
// @Summary Acknowledge or silence an alert
// @Description Acknowledging removes an alert from the notifiable count while leaving it visible. Silencing suppresses notifications for a bounded window without stopping evaluation.
// @Tags admin
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Alert ID"
// @Param request body patchAlertRequest true "Acknowledge flag or silence duration in minutes"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /admin/alerts/{id} [patch]
func (h *AdminAlertHandler) Patch(c *fiber.Ctx) error {
	alertID := c.Params("id")
	if alertID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "alert id is required"})
	}

	var req patchAlertRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Ack == nil && req.SilenceMinutes == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "one of ack or silence_minutes is required",
		})
	}

	ctx := context.Background()
	adminID, _ := c.Locals("admin_id").(string)

	if req.Ack != nil {
		alert, err := h.alerts.Acknowledge(ctx, alertID, adminID, *req.Ack)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		if *req.Ack {
			h.activity.Log(ctx, services.Record{
				EventType:  services.EventNodeAlertAcked,
				Severity:   services.SeverityInfo,
				ActorType:  "admin",
				ActorID:    adminID,
				ActorLabel: alert.NodeName,
				TargetType: "node",
				TargetID:   alert.NodeID,
				Detail:     map[string]any{"node": alert.NodeName, "kind": alert.Kind},
			})
		}
	}

	if req.SilenceMinutes != nil {
		minutes := *req.SilenceMinutes
		if minutes < 0 || minutes > silenceMaxMinutes {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "silence_minutes must be between 0 and 1440",
			})
		}
		alert, err := h.alerts.Silence(ctx, alertID, minutes)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
		}
		if minutes > 0 {
			h.activity.Log(ctx, services.Record{
				EventType:  services.EventNodeAlertSilenced,
				Severity:   services.SeverityInfo,
				ActorType:  "admin",
				ActorID:    adminID,
				ActorLabel: alert.NodeName,
				TargetType: "node",
				TargetID:   alert.NodeID,
				Detail: map[string]any{
					"node": alert.NodeName, "kind": alert.Kind, "minutes": minutes,
				},
			})
		}
	}

	return c.JSON(fiber.Map{"status": "ok"})
}
