package handlers

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// GetAccountSubscription serves the single account subscription URL. Every
// client of the account gets the account's one UUID; the server never trusts
// client-supplied identity headers (x-hwid included) to tell devices apart.
// Concurrent devices are counted from what the nodes observe instead.
func (h *SubscriptionHandler) GetAccountSubscription(c *fiber.Ctx) error {
	return h.getAccountSubscription(c, "")
}

func (h *SubscriptionHandler) getAccountSubscription(c *fiber.Ctx, formatOverride string) error {
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return accountSubscriptionError(c)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Row lock serialises concurrent first fetches so exactly one device is
	// created for a new account regardless of how many clients race at once.
	var user subscriptionUser
	err = tx.QueryRow(ctx, `SELECT u.id::text, u.plan_id::text, u.is_active, u.status, u.traffic_used,
 p.traffic_limit, p.speed_limit, u.plan_expires_at, u.language
 FROM users u LEFT JOIN plans p ON p.id = u.plan_id
 WHERE u.sub_token = $1 FOR UPDATE OF u`, c.Params("sub_token")).Scan(
		&user.ID, &user.PlanID, &user.IsActive, &user.Status, &user.TrafficUsed,
		&user.TrafficLimit, &user.SpeedLimit, &user.PlanExpiresAt, &user.Language)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}
	if err != nil {
		return accountSubscriptionError(c)
	}
	if status, message := subscriptionEligibility(user); status != 0 {
		return c.Status(status).JSON(fiber.Map{"error": message})
	}

	// Reuse the account's oldest live device or create it on first fetch.
	var deviceID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM devices WHERE user_id = $1 AND retired_at IS NULL
 ORDER BY created_at, id LIMIT 1`, user.ID).Scan(&deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		deviceID, err = h.createAccountDevice(ctx, tx, user.ID)
	}
	if err != nil {
		// Stop before Commit: the deferred Rollback discards any write made
		// before the failure, and no subscription is served.
		return sendAccountSubscriptionError(c, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return accountSubscriptionError(c)
	}
	return h.getSubscriptionForDevice(c, deviceID, formatOverride)
}

// createAccountDevice inserts the account's device.
func (h *SubscriptionHandler) createAccountDevice(ctx context.Context, tx pgx.Tx, userID string) (string, error) {
	privateKey, publicKey, err := crypto.GenerateWireGuardKeypair()
	if err != nil {
		return "", err
	}
	var index int64
	if err := tx.QueryRow(ctx, `SELECT nextval('wg_ip_seq')`).Scan(&index); err != nil {
		return "", err
	}
	var deviceID string
	err = tx.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid, wg_private_key, wg_public_key, wg_address)
		 VALUES ($1, 'Account', $2, $3, $4, $5) RETURNING id::text`,
		userID, crypto.NewUUID(), privateKey, publicKey, wgAddressForIndex(index),
	).Scan(&deviceID)
	if err != nil {
		return "", err
	}
	return deviceID, nil
}

// sendAccountSubscriptionError writes a device-resolution failure: a
// *fiber.Error keeps its status and message, anything else is the generic 500
// so database details never reach the client.
func sendAccountSubscriptionError(c *fiber.Ctx, err error) error {
	var clientError *fiber.Error
	if errors.As(err, &clientError) {
		return c.Status(clientError.Code).JSON(fiber.Map{"error": clientError.Message})
	}
	return accountSubscriptionError(c)
}

func accountSubscriptionError(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to resolve subscription"})
}
