package handlers

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminFreeTrialHandler struct{ db *pgxpool.Pool }

func NewAdminFreeTrialHandler(db *pgxpool.Pool) *AdminFreeTrialHandler {
	return &AdminFreeTrialHandler{db: db}
}

type freeTrialRequest struct {
	Code                  string  `json:"code"`
	PlanID                *string `json:"plan_id"`
	DurationDays          int     `json:"duration_days"`
	TrialDays             int     `json:"trial_days"`
	MaxRedemptions        *int    `json:"max_redemptions"`
	MaxRedemptionsPerUser int     `json:"max_redemptions_per_user"`
	RequiresCard          *bool   `json:"requires_card"`
	IsActive              *bool   `json:"is_active"`
}

type freeTrialResponse struct {
	ID                    string    `json:"id"`
	Code                  string    `json:"code"`
	PlanID                *string   `json:"plan_id"`
	DurationDays          int       `json:"duration_days"`
	TrialDays             int       `json:"trial_days"`
	MaxRedemptions        *int      `json:"max_redemptions"`
	MaxRedemptionsPerUser int       `json:"max_redemptions_per_user"`
	RequiresCard          bool      `json:"requires_card"`
	IsActive              bool      `json:"is_active"`
	CreatedAt             time.Time `json:"created_at"`
}

const freeTrialColumns = `id, code, plan_id, duration_days, trial_days, max_redemptions,
	max_redemptions_per_user, requires_card, is_active, created_at`

func scanFreeTrial(row pgx.Row) (freeTrialResponse, error) {
	var p freeTrialResponse
	err := row.Scan(&p.ID, &p.Code, &p.PlanID, &p.DurationDays, &p.TrialDays, &p.MaxRedemptions,
		&p.MaxRedemptionsPerUser, &p.RequiresCard, &p.IsActive, &p.CreatedAt)
	return p, err
}

func (h *AdminFreeTrialHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(c.UserContext(), `SELECT `+freeTrialColumns+` FROM free_trial_promotions ORDER BY created_at DESC, id`)
	if err != nil {
		return promotionKindError(c, err)
	}
	defer rows.Close()
	result := make([]freeTrialResponse, 0)
	for rows.Next() {
		p, err := scanFreeTrial(rows)
		if err != nil {
			return promotionKindError(c, err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return promotionKindError(c, err)
	}
	return c.JSON(result)
}

func validateFreeTrial(req *freeTrialRequest) error {
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" || req.DurationDays <= 0 || req.TrialDays <= 0 {
		return errors.New("code and positive duration_days and trial_days are required")
	}
	if req.MaxRedemptions != nil && *req.MaxRedemptions <= 0 {
		return errors.New("max_redemptions must be positive")
	}
	if req.MaxRedemptionsPerUser == 0 {
		req.MaxRedemptionsPerUser = 1
	}
	if req.MaxRedemptionsPerUser < 1 {
		return errors.New("max_redemptions_per_user must be positive")
	}
	if req.PlanID != nil {
		if _, err := uuid.Parse(*req.PlanID); err != nil {
			return errors.New("invalid plan_id")
		}
	}
	return nil
}

func (h *AdminFreeTrialHandler) Create(c *fiber.Ctx) error { return h.save(c, false) }
func (h *AdminFreeTrialHandler) Update(c *fiber.Ctx) error { return h.save(c, true) }

func (h *AdminFreeTrialHandler) save(c *fiber.Ctx, update bool) error {
	if update && !promotionKindID(c, "id") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}
	var req freeTrialRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	if err := validateFreeTrial(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	args := []any{req.Code, req.PlanID, req.DurationDays, req.TrialDays, req.MaxRedemptions,
		req.MaxRedemptionsPerUser, defaultTrue(req.RequiresCard), defaultTrue(req.IsActive)}
	query := `INSERT INTO free_trial_promotions (code, plan_id, duration_days, trial_days, max_redemptions,
		max_redemptions_per_user, requires_card, is_active) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	status := fiber.StatusCreated
	if update {
		query = `UPDATE free_trial_promotions SET code=$1,plan_id=$2,duration_days=$3,trial_days=$4,
			max_redemptions=$5,max_redemptions_per_user=$6,requires_card=$7,is_active=$8 WHERE id=$9`
		args = append(args, c.Params("id"))
		status = fiber.StatusOK
	}
	p, err := scanFreeTrial(h.db.QueryRow(c.UserContext(), query+` RETURNING `+freeTrialColumns, args...))
	if err != nil {
		return promotionKindError(c, err)
	}
	return c.Status(status).JSON(p)
}

func (h *AdminFreeTrialHandler) Delete(c *fiber.Ctx) error {
	if !promotionKindID(c, "id") {
		return c.Status(400).JSON(fiber.Map{"error": "invalid id"})
	}
	tag, err := h.db.Exec(c.UserContext(), `DELETE FROM free_trial_promotions WHERE id=$1`, c.Params("id"))
	if err != nil {
		return promotionKindError(c, err)
	}
	if tag.RowsAffected() == 0 {
		return c.Status(404).JSON(fiber.Map{"error": "promotion not found"})
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func defaultTrue(value *bool) bool { return value == nil || *value }

func promotionKindID(c *fiber.Ctx, param string) bool {
	_, err := uuid.Parse(c.Params(param))
	return err == nil
}

func promotionKindError(c *fiber.Ctx, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return c.Status(404).JSON(fiber.Map{"error": "promotion not found"})
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return c.Status(409).JSON(fiber.Map{"error": "code already exists"})
		case "23503":
			return c.Status(409).JSON(fiber.Map{"error": "referenced record does not exist or is still in use"})
		case "23514", "22003":
			return c.Status(400).JSON(fiber.Map{"error": "invalid promotion values"})
		}
	}
	return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
}
