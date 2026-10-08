package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// GetAccountSubscription serves the single account subscription URL. Every
// client of the account shares one device UUID, so no client-supplied identity
// (such as x-hwid) is required or read. Concurrent use is limited from what the
// node pool observes online, not from how many apps imported the URL. The
// output format follows the requesting client (see detectClientFormat).
func (h *SubscriptionHandler) GetAccountSubscription(c *fiber.Ctx) error {
	return h.getAccountSubscription(c, "")
}

func (h *SubscriptionHandler) getAccountSubscription(c *fiber.Ctx, formatOverride string) error {
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return accountSubscriptionError(c)
	}
	defer tx.Rollback(ctx)

	// The row lock serializes concurrent first fetches so an account without a
	// device gets exactly one, not one per racing request.
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

	// Reuse the account's oldest live device so existing accounts keep the UUID
	// their nodes already admit; only an account with none gets a new one.
	var deviceID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM devices WHERE user_id = $1 AND retired_at IS NULL
 ORDER BY created_at, id LIMIT 1`, user.ID).Scan(&deviceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return accountSubscriptionError(c)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		privateKey, publicKey, err := crypto.GenerateWireGuardKeypair()
		if err != nil {
			return accountSubscriptionError(c)
		}
		var index int64
		if err := tx.QueryRow(ctx, `SELECT nextval('wg_ip_seq')`).Scan(&index); err != nil {
			return accountSubscriptionError(c)
		}
		err = tx.QueryRow(ctx, `INSERT INTO devices (user_id, name, xray_uuid, wg_private_key, wg_public_key, wg_address)
 VALUES ($1, 'Account', $2, $3, $4, $5) RETURNING id::text`,
			user.ID, crypto.NewUUID(), privateKey, publicKey, wgAddressForIndex(index)).Scan(&deviceID)
		if err != nil {
			return accountSubscriptionError(c)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return accountSubscriptionError(c)
	}
	return h.getSubscriptionForDevice(c, deviceID, formatOverride)
}

func accountSubscriptionError(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to resolve subscription"})
}
