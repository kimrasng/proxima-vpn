package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/proximavpn/proxima-vpn/pkg/crypto"
)

// hwidPattern accepts 10–64 printable non-space ASCII characters, which is the
// full range of sane client-supplied installation identifiers.
var hwidPattern = regexp.MustCompile(`^[\x21-\x7E]{10,64}$`)

// validateHWID returns the raw header value only if it matches the accepted
// format. An empty or malformed value returns ("", false).
func validateHWID(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	for _, r := range raw {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return "", false
		}
	}
	if !hwidPattern.MatchString(raw) {
		return "", false
	}
	return raw, true
}

// hwidFingerprint derives a keyed fingerprint from the raw HWID and the
// per-deployment pepper stored in hwid_secrets. Clients never see or supply
// this value; it is computed server-side from raw client headers.
func hwidFingerprint(pepper, rawHWID string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(rawHWID))
	return hex.EncodeToString(mac.Sum(nil))
}

// GetAccountSubscription serves the single account subscription URL. When the
// request carries a valid x-hwid header the server issues a stable UUID slot
// per installation (up to the hwid_registration_cap). Without x-hwid the
// account falls back to its single shared UUID, preserving legacy behaviour.
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

	rawHWID, hasHWID := validateHWID(c.Get("x-hwid"))

	var deviceID string
	if hasHWID {
		deviceID, err = h.resolveHWIDDevice(c, tx, user.ID, rawHWID)
		if err != nil {
			return err // already a fiber response
		}
	} else {
		// Legacy path: reuse the account's oldest live device or create one.
		err = tx.QueryRow(ctx, `SELECT id::text FROM devices WHERE user_id = $1 AND retired_at IS NULL
 ORDER BY created_at, id LIMIT 1`, user.ID).Scan(&deviceID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return accountSubscriptionError(c)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			deviceID, err = h.createAccountDevice(c, tx, user.ID)
			if err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return accountSubscriptionError(c)
	}
	return h.getSubscriptionForDevice(c, deviceID, formatOverride)
}

// resolveHWIDDevice finds or creates the device slot for a validated HWID. It
// enforces the per-account registration cap from the settings table and updates
// the last-seen timestamp on every successful refresh.
func (h *SubscriptionHandler) resolveHWIDDevice(c *fiber.Ctx, tx pgx.Tx, userID, rawHWID string) (string, error) {
	ctx := c.UserContext()

	// Load the server pepper once per request; it never changes after init.
	var pepper string
	if err := tx.QueryRow(ctx, `SELECT pepper FROM hwid_secrets WHERE id = true`).Scan(&pepper); err != nil {
		return "", accountSubscriptionError(c)
	}
	fp := hwidFingerprint(pepper, rawHWID)

	// Fast path: device already registered for this fingerprint.
	var deviceID string
	err := tx.QueryRow(ctx,
		`UPDATE devices SET last_subscription_at = NOW()
		 WHERE user_id = $1 AND hwid_fingerprint = $2 AND retired_at IS NULL
		 RETURNING id::text`, userID, fp).Scan(&deviceID)
	if err == nil {
		return deviceID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", accountSubscriptionError(c)
	}

	// New fingerprint: check registration cap.
	var cap int
	if err := tx.QueryRow(ctx,
		`SELECT value::int FROM settings WHERE key = 'hwid_registration_cap'`).Scan(&cap); err != nil {
		cap = 10 // safe default if settings row is missing
	}
	var active int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM devices WHERE user_id = $1 AND hwid_fingerprint IS NOT NULL AND retired_at IS NULL`,
		userID).Scan(&active); err != nil {
		return "", accountSubscriptionError(c)
	}
	if active >= cap {
		return "", c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "hwid registration cap reached"})
	}

	// Register a new slot for this fingerprint.
	deviceID, err = h.createHWIDDevice(c, tx, userID, fp)
	if err != nil {
		return "", err
	}
	return deviceID, nil
}

// createHWIDDevice inserts a new device row with a fingerprint and all required
// fields, using the same WireGuard address pool as manual device registration.
func (h *SubscriptionHandler) createHWIDDevice(c *fiber.Ctx, tx pgx.Tx, userID, fp string) (string, error) {
	ctx := c.UserContext()
	privateKey, publicKey, err := crypto.GenerateWireGuardKeypair()
	if err != nil {
		return "", accountSubscriptionError(c)
	}
	var index int64
	if err := tx.QueryRow(ctx, `SELECT nextval('wg_ip_seq')`).Scan(&index); err != nil {
		return "", accountSubscriptionError(c)
	}
	var deviceID string
	err = tx.QueryRow(ctx,
		`INSERT INTO devices
		   (user_id, name, xray_uuid, wg_private_key, wg_public_key, wg_address,
		    hwid_fingerprint, first_subscription_at, last_subscription_at)
		 VALUES ($1, 'HWID', $2, $3, $4, $5, $6, NOW(), NOW())
		 RETURNING id::text`,
		userID, crypto.NewUUID(), privateKey, publicKey, wgAddressForIndex(index), fp,
	).Scan(&deviceID)
	if err != nil {
		return "", accountSubscriptionError(c)
	}
	return deviceID, nil
}

// createAccountDevice inserts the single legacy account device (no fingerprint).
func (h *SubscriptionHandler) createAccountDevice(c *fiber.Ctx, tx pgx.Tx, userID string) (string, error) {
	ctx := c.UserContext()
	privateKey, publicKey, err := crypto.GenerateWireGuardKeypair()
	if err != nil {
		return "", accountSubscriptionError(c)
	}
	var index int64
	if err := tx.QueryRow(ctx, `SELECT nextval('wg_ip_seq')`).Scan(&index); err != nil {
		return "", accountSubscriptionError(c)
	}
	var deviceID string
	err = tx.QueryRow(ctx,
		`INSERT INTO devices (user_id, name, xray_uuid, wg_private_key, wg_public_key, wg_address)
		 VALUES ($1, 'Account', $2, $3, $4, $5) RETURNING id::text`,
		userID, crypto.NewUUID(), privateKey, publicKey, wgAddressForIndex(index),
	).Scan(&deviceID)
	if err != nil {
		return "", accountSubscriptionError(c)
	}
	return deviceID, nil
}

func accountSubscriptionError(c *fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to resolve subscription"})
}
