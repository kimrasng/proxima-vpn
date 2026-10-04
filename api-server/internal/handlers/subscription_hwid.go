package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

var validHWID = regexp.MustCompile(`^[A-Za-z0-9=-]{10,64}$`)

// GetHWIDSubscription resolves an account-scoped installation before using the
// same eligibility, node selection and format negotiation as the legacy route.
func (h *SubscriptionHandler) GetHWIDSubscription(c *fiber.Ctx) error {
	hwid := c.Get("x-hwid")
	if !validHWID.MatchString(hwid) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "valid x-hwid header required"})
	}
	ctx := c.UserContext()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return hwidInternalError(c)
	}
	defer tx.Rollback(ctx)

	var user subscriptionUser
	err = tx.QueryRow(ctx, `SELECT u.id, u.plan_id, u.is_active, u.status, u.traffic_used,
 p.traffic_limit, p.speed_limit, u.plan_expires_at, u.language
 FROM users u LEFT JOIN plans p ON p.id = u.plan_id
 WHERE u.sub_token = $1 FOR UPDATE OF u`, c.Params("sub_token")).Scan(
		&user.ID, &user.PlanID, &user.IsActive, &user.Status, &user.TrafficUsed,
		&user.TrafficLimit, &user.SpeedLimit, &user.PlanExpiresAt, &user.Language)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "subscription not found"})
	}
	if err != nil {
		return hwidInternalError(c)
	}
	if status, message := subscriptionEligibility(user); status != 0 {
		return c.Status(status).JSON(fiber.Map{"error": message})
	}

	var pepper string
	if err := tx.QueryRow(ctx, `SELECT pepper FROM hwid_secrets WHERE id = true`).Scan(&pepper); err != nil {
		return hwidInternalError(c)
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(hwid)) // exact case is significant; neither raw nor reversible form is stored
	fingerprint := hex.EncodeToString(mac.Sum(nil))

	var deviceID string
	var retiredAt *time.Time
	err = tx.QueryRow(ctx, `SELECT id, retired_at FROM devices WHERE user_id = $1 AND hwid_fingerprint = $2`, user.ID, fingerprint).Scan(&deviceID, &retiredAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return hwidInternalError(c)
	}
	if err == nil && retiredAt != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "device registration retired"})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		cap := 10
		var configured string
		err = tx.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'hwid_registration_cap'`).Scan(&configured)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return hwidInternalError(c)
		}
		if err == nil {
			parsed, parseErr := strconv.Atoi(configured)
			if parseErr != nil || parsed < 1 {
				return hwidInternalError(c)
			}
			cap = parsed
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM devices WHERE user_id = $1 AND hwid_fingerprint IS NOT NULL AND retired_at IS NULL`, user.ID).Scan(&count); err != nil {
			return hwidInternalError(c)
		}
		if count >= cap {
			c.Set("X-HWID-Registration", "cap-reached")
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "registered HWID limit reached; remove an unused device or contact support"})
		}
		privateKey, publicKey, err := crypto.GenerateWireGuardKeypair()
		if err != nil {
			return hwidInternalError(c)
		}
		var index int64
		if err := tx.QueryRow(ctx, `SELECT nextval('wg_ip_seq')`).Scan(&index); err != nil {
			return hwidInternalError(c)
		}
		err = tx.QueryRow(ctx, `INSERT INTO devices
   (user_id, name, xray_uuid, wg_private_key, wg_public_key, wg_address, hwid_fingerprint, first_subscription_at, last_subscription_at)
   VALUES ($1, 'HWID device', $2, $3, $4, $5, $6, NOW(), NOW()) RETURNING id`,
			user.ID, crypto.NewUUID(), privateKey, publicKey, wgAddressForIndex(index), fingerprint).Scan(&deviceID)
		if err != nil {
			return hwidInternalError(c)
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE devices SET last_subscription_at = NOW() WHERE id = $1`, deviceID); err != nil {
			return hwidInternalError(c)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return hwidInternalError(c)
	}
	return h.getSubscriptionForDevice(c, deviceID)
}

func hwidInternalError(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to resolve subscription device"})
}
