package handlers

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type realityTargetRequest struct {
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
}

// probeRealityTarget requires the TLS 1.3 + X25519 handshake Reality relies on.
// It runs from the panel's network, so it cannot prove reachability from a
// node with different routing; it rejects targets that are plainly unusable.
func probeRealityTarget(ctx context.Context, host string, port int) error {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config: &tls.Config{
			ServerName:       host,
			MinVersion:       tls.VersionTLS13,
			CurvePreferences: []tls.CurveID{tls.X25519},
			NextProtos:       []string{"h2", "http/1.1"},
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return conn.Close()
}

// SetRealityTarget changes a node's Reality camouflage target (node SNI and
// every enabled Reality listener's dest/server_names) in one transaction.
// PUT /api/v1/admin/nodes/:id/reality-target
func (h *AdminNodeHandler) SetRealityTarget(c *fiber.Ctx) error {
	id := c.Params("id")
	var req realityTargetRequest
	if err := c.BodyParser(&req); err != nil || req.Hostname == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "hostname is required"})
	}
	probe := h.realityProbe
	if probe == nil {
		probe = probeRealityTarget
	}
	ctx := c.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update node"})
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	locked, err := services.LockRealityNode(ctx, tx, id)
	if err != nil {
		return nodeSNIErrorResponse(c, err)
	}
	host, err := services.ChangeRealityTarget(ctx, tx, locked, req.Hostname, req.Port, probe)
	if err != nil {
		var unreachable *services.RealityTargetUnreachableError
		if errors.As(err, &unreachable) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
				"error":  "Reality target failed the TLS 1.3/X25519 check",
				"detail": unreachable.Cause.Error(),
			})
		}
		return nodeSNIErrorResponse(c, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update node"})
	}
	h.activity.Log(context.Background(), services.Record{
		EventType:  services.EventNodeUpdated,
		Severity:   services.SeverityInfo,
		ActorType:  "admin",
		ActorID:    adminIDOf(c),
		ActorLabel: adminEmail(c),
		TargetType: "node",
		TargetID:   id,
		Detail:     map[string]any{"reality_target": host},
	})
	return c.JSON(fiber.Map{"reality_client_sni": host})
}
