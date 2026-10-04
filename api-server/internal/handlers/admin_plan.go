package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminPlanHandler handles admin plan CRUD endpoints.
type AdminPlanHandler struct {
	db *pgxpool.Pool
}

// NewAdminPlanHandler creates a new AdminPlanHandler.
func NewAdminPlanHandler(db *pgxpool.Pool) *AdminPlanHandler {
	return &AdminPlanHandler{db: db}
}

// pgxQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, so the
// prices/features helpers below run identically whether called for a
// standalone read (Get) or inside the Create/Update transaction.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type planPriceInput struct {
	DurationDays int   `json:"duration_days"`
	PriceCents   int64 `json:"price_cents"`
}

type planFeatureInput struct {
	Included bool              `json:"included"`
	Text     map[string]string `json:"text"`
}

type createPlanRequest struct {
	ID            *string            `json:"id"`
	Advertise     bool               `json:"advertise"`
	IsAdvertised  bool               `json:"is_advertised"`
	IsActive      *bool              `json:"is_active"`
	Name          string             `json:"name"`
	TrafficLimit  *int64             `json:"traffic_limit"`
	DurationDays  int                `json:"duration_days"`
	MaxDevices    int                `json:"max_devices"`
	MaxConcurrent *int               `json:"max_concurrent"`
	SpeedLimit    *int               `json:"speed_limit"`
	NodeGroupID   string             `json:"node_group_id"`
	Prices        []planPriceInput   `json:"prices"`
	Features      []planFeatureInput `json:"features"`
}

// nullablePlanLimit distinguishes an omitted PATCH field from explicit null.
type nullablePlanLimit[T int | int64] struct {
	Present bool
	Value   *T
}

func (v *nullablePlanLimit[T]) UnmarshalJSON(data []byte) error {
	v.Present = true
	return json.Unmarshal(data, &v.Value)
}

type updatePlanRequest struct {
	Advertise     *bool                    `json:"advertise"`
	IsAdvertised  *bool                    `json:"is_advertised"`
	Name          *string                  `json:"name"`
	TrafficLimit  nullablePlanLimit[int64] `json:"traffic_limit"`
	DurationDays  *int                     `json:"duration_days"`
	MaxDevices    *int                     `json:"max_devices"`
	MaxConcurrent nullablePlanLimit[int]   `json:"max_concurrent"`
	SpeedLimit    nullablePlanLimit[int]   `json:"speed_limit"`
	NodeGroupID   *string                  `json:"node_group_id"`
	IsActive      *bool                    `json:"is_active"`
	// Nil leaves prices/features untouched; a present (possibly empty) slice
	// fully replaces the set, matching the node_labels convention except that
	// here there is no per-item "empty clears it" - the whole list is the unit.
	Prices   []planPriceInput   `json:"prices"`
	Features []planFeatureInput `json:"features"`
}

type planResponse struct {
	Advertise     bool               `json:"advertise"`
	IsAdvertised  bool               `json:"is_advertised"`
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	TrafficLimit  *int64             `json:"traffic_limit"`
	DurationDays  int                `json:"duration_days"`
	MaxDevices    int                `json:"max_devices"`
	MaxConcurrent *int               `json:"max_concurrent"`
	SpeedLimit    *int               `json:"speed_limit"`
	NodeGroupID   string             `json:"node_group_id"`
	NodeGroupName *string            `json:"node_group_name"`
	IsActive      bool               `json:"is_active"`
	CreatedAt     time.Time          `json:"created_at"`
	Prices        []planPriceInput   `json:"prices"`
	Features      []planFeatureInput `json:"features"`
	// Derived from Prices, never a stored column: a plan with zero priced
	// durations cannot enter the order flow, but stays admin-assignable via
	// Telegram/admin_user.go regardless of this value.
	Purchasable bool `json:"purchasable"`
}

// Validate before opening a transaction so bad editor input returns 400 rather
// than a constraint error after prices/features have begun replacement.
func validatePlanInput(name *string, traffic *int64, duration, devices, concurrent, speed *int, group *string, prices []planPriceInput, features []planFeatureInput) error {
	if name != nil && (strings.TrimSpace(*name) == "" || strings.ContainsRune(*name, 0)) {
		return fmt.Errorf("name is required")
	}
	for _, field := range []struct {
		name  string
		value *int
	}{{"duration_days", duration}, {"max_devices", devices}, {"max_concurrent", concurrent}} {
		if field.value != nil && (*field.value <= 0 || int64(*field.value) > math.MaxInt32) {
			return fmt.Errorf("%s must be a positive 32-bit integer", field.name)
		}
	}
	if traffic != nil && *traffic < 0 {
		return fmt.Errorf("traffic_limit must not be negative")
	}
	if speed != nil && (*speed < 0 || int64(*speed) > math.MaxInt32) {
		return fmt.Errorf("speed_limit must be a non-negative 32-bit integer")
	}
	if group != nil {
		var id pgtype.UUID
		if strings.TrimSpace(*group) == "" {
			return fmt.Errorf("node_group_id is required")
		}
		if err := id.Scan(*group); err != nil {
			return fmt.Errorf("node_group_id must be a UUID")
		}
	}
	seen := make(map[int]bool, len(prices))
	for _, price := range prices {
		if price.DurationDays <= 0 || int64(price.DurationDays) > math.MaxInt32 {
			return fmt.Errorf("price duration_days must be a positive 32-bit integer")
		}
		if price.PriceCents < 0 {
			return fmt.Errorf("price_cents must not be negative")
		}
		if seen[price.DurationDays] {
			return fmt.Errorf("price duration_days must be unique")
		}
		seen[price.DurationDays] = true
	}
	for _, feature := range features {
		hasText := false
		for language, text := range feature.Text {
			if strings.ContainsRune(language, 0) || strings.ContainsRune(text, 0) {
				return fmt.Errorf("feature language and text must not contain null characters")
			}
			if strings.TrimSpace(language) == "" {
				return fmt.Errorf("feature language is required")
			}
			if strings.TrimSpace(text) != "" {
				hasText = true
			}
		}
		if !hasText {
			return fmt.Errorf("feature text is required")
		}
	}
	return nil
}

// planPrices returns every priced duration for planID, ordered shortest
// first so the admin editor and the user-facing card list render consistently.
func planPrices(ctx context.Context, q pgxQuerier, planID string) ([]planPriceInput, error) {
	rows, err := q.Query(ctx,
		`SELECT duration_days, price_cents FROM plan_prices WHERE plan_id = $1 ORDER BY duration_days`,
		planID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := make([]planPriceInput, 0)
	for rows.Next() {
		var p planPriceInput
		if err := rows.Scan(&p.DurationDays, &p.PriceCents); err != nil {
			return nil, err
		}
		prices = append(prices, p)
	}
	return prices, rows.Err()
}

// planFeatures returns every bullet for planID in display order, each
// carrying its authored text for every language it has one.
func planFeatures(ctx context.Context, q pgxQuerier, planID string) ([]planFeatureInput, error) {
	rows, err := q.Query(ctx,
		`SELECT position, included FROM plan_features WHERE plan_id = $1 ORDER BY position`,
		planID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var positions []int
	features := make([]planFeatureInput, 0)
	for rows.Next() {
		var pos int
		var f planFeatureInput
		if err := rows.Scan(&pos, &f.Included); err != nil {
			return nil, err
		}
		f.Text = make(map[string]string)
		positions = append(positions, pos)
		features = append(features, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(features) == 0 {
		return features, nil
	}

	textRows, err := q.Query(ctx,
		`SELECT position, language, text FROM plan_feature_texts WHERE plan_id = $1`,
		planID,
	)
	if err != nil {
		return nil, err
	}
	defer textRows.Close()

	byPosition := make(map[int]int, len(positions))
	for i, pos := range positions {
		byPosition[pos] = i
	}
	for textRows.Next() {
		var pos int
		var lang, text string
		if err := textRows.Scan(&pos, &lang, &text); err != nil {
			return nil, err
		}
		if i, ok := byPosition[pos]; ok {
			features[i].Text[lang] = text
		}
	}
	return features, textRows.Err()
}

// replacePlanPrices fully replaces planID's pricing matrix with in, inside
// the caller's transaction. Deleting first rather than diffing keeps a
// duration removed from the submitted list from lingering as stale pricing.
func replacePlanPrices(ctx context.Context, tx pgx.Tx, planID string, in []planPriceInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM plan_prices WHERE plan_id = $1`, planID); err != nil {
		return err
	}
	for _, p := range in {
		if _, err := tx.Exec(ctx,
			`INSERT INTO plan_prices (plan_id, duration_days, price_cents) VALUES ($1, $2, $3)`,
			planID, p.DurationDays, p.PriceCents,
		); err != nil {
			return err
		}
	}
	return nil
}

// replacePlanFeatures fully replaces planID's feature bullets with in, in the
// given order. position and included are per-bullet, not per-language - see
// schema.go for why plan_features/plan_feature_texts are two tables rather
// than one keyed by language.
func replacePlanFeatures(ctx context.Context, tx pgx.Tx, planID string, in []planFeatureInput) error {
	// plan_feature_texts cascades off plan_features's FK.
	if _, err := tx.Exec(ctx, `DELETE FROM plan_features WHERE plan_id = $1`, planID); err != nil {
		return err
	}
	for i, f := range in {
		if _, err := tx.Exec(ctx,
			`INSERT INTO plan_features (plan_id, position, included) VALUES ($1, $2, $3)`,
			planID, i, f.Included,
		); err != nil {
			return err
		}
		for lang, text := range f.Text {
			if text == "" {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO plan_feature_texts (plan_id, position, language, text) VALUES ($1, $2, $3, $4)`,
				planID, i, lang, text,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadPlanExtras fills a planResponse's Prices, Features and derived
// Purchasable fields. Shared by Get, Create and Update so all three return
// the identical shape.
func loadPlanExtras(ctx context.Context, q pgxQuerier, plan *planResponse) error {
	prices, err := planPrices(ctx, q, plan.ID)
	if err != nil {
		return err
	}
	features, err := planFeatures(ctx, q, plan.ID)
	if err != nil {
		return err
	}
	plan.Prices = prices
	plan.Features = features
	plan.Purchasable = len(prices) > 0
	return nil
}

// Create creates a new plan.
// @Summary Create plan
// @Description Creates a new subscription plan
// @Tags admin-plans
// @Accept json
// @Produce json
// @Param body body createPlanRequest true "Plan details"
// @Success 201 {object} planResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans [post]
func (h *AdminPlanHandler) Create(c *fiber.Ctx) error {
	var req createPlanRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if err := validatePlanInput(&req.Name, req.TrafficLimit, &req.DurationDays, &req.MaxDevices, req.MaxConcurrent, req.SpeedLimit, &req.NodeGroupID, req.Prices, req.Features); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	var suppliedID pgtype.UUID
	if req.ID != nil {
		if err := suppliedID.Scan(*req.ID); err != nil || !suppliedID.Valid {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id must be a UUID"})
		}
	}

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create plan",
		})
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var plan planResponse
	err = tx.QueryRow(ctx,
		`INSERT INTO plans (name, traffic_limit, duration_days, max_devices, max_concurrent, speed_limit, node_group_id, id, advertise, is_active, is_advertised)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::uuid, gen_random_uuid()), $9, COALESCE($10::boolean, true), false)
		 RETURNING id, name, traffic_limit, duration_days, max_devices, max_concurrent, speed_limit, node_group_id, is_active, advertise, is_advertised, created_at`,
		req.Name, req.TrafficLimit, req.DurationDays, req.MaxDevices, req.MaxConcurrent, req.SpeedLimit, req.NodeGroupID, suppliedID, req.Advertise, req.IsActive,
	).Scan(&plan.ID, &plan.Name, &plan.TrafficLimit, &plan.DurationDays, &plan.MaxDevices, &plan.MaxConcurrent, &plan.SpeedLimit, &plan.NodeGroupID, &plan.IsActive, &plan.Advertise, &plan.IsAdvertised, &plan.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "plan id already exists"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create plan",
		})
	}

	if err := replacePlanPrices(ctx, tx, plan.ID, req.Prices); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create plan prices",
		})
	}
	if err := replacePlanFeatures(ctx, tx, plan.ID, req.Features); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create plan features",
		})
	}
	if err := loadPlanExtras(ctx, tx, &plan); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to load plan pricing",
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create plan",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(plan)
}

// List returns all plans with node group info.
// @Summary List plans
// @Description Returns all plans with node group information
// @Tags admin-plans
// @Produce json
// @Success 200 {array} planResponse
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans [get]
func (h *AdminPlanHandler) List(c *fiber.Ctx) error {
	rows, err := h.db.Query(
		context.Background(),
		`SELECT p.id, p.name, p.traffic_limit, p.duration_days, p.max_devices, p.max_concurrent, p.speed_limit,
		        p.node_group_id, ng.name AS node_group_name, p.is_active, p.advertise, p.is_advertised, p.created_at,
		        EXISTS (SELECT 1 FROM plan_prices pp WHERE pp.plan_id = p.id) AS purchasable
		 FROM plans p
		 LEFT JOIN node_groups ng ON p.node_group_id = ng.id
		 ORDER BY p.created_at DESC`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list plans",
		})
	}
	defer rows.Close()

	// Prices/features are loaded per-plan only by Get, matching the
	// node_labels convention: the list view omits them rather than joining
	// for every row.
	plans := make([]planResponse, 0)
	for rows.Next() {
		var p planResponse
		if err := rows.Scan(&p.ID, &p.Name, &p.TrafficLimit, &p.DurationDays, &p.MaxDevices, &p.MaxConcurrent, &p.SpeedLimit, &p.NodeGroupID, &p.NodeGroupName, &p.IsActive, &p.Advertise, &p.IsAdvertised, &p.CreatedAt, &p.Purchasable); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to scan plan",
			})
		}
		plans = append(plans, p)
	}

	return c.JSON(plans)
}

// Get returns a single plan by ID with node group info, pricing and features.
// @Summary Get plan
// @Description Returns a single plan by ID with node group info, pricing and feature bullets
// @Tags admin-plans
// @Produce json
// @Param id path string true "Plan ID"
// @Success 200 {object} planResponse
// @Failure 404 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans/{id} [get]
func (h *AdminPlanHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	ctx := context.Background()

	var plan planResponse
	err := h.db.QueryRow(
		ctx,
		`SELECT p.id, p.name, p.traffic_limit, p.duration_days, p.max_devices, p.max_concurrent, p.speed_limit,
		        p.node_group_id, ng.name AS node_group_name, p.is_active, p.advertise, p.is_advertised, p.created_at
		 FROM plans p
		 LEFT JOIN node_groups ng ON p.node_group_id = ng.id
		 WHERE p.id = $1`,
		id,
	).Scan(&plan.ID, &plan.Name, &plan.TrafficLimit, &plan.DurationDays, &plan.MaxDevices, &plan.MaxConcurrent, &plan.SpeedLimit, &plan.NodeGroupID, &plan.NodeGroupName, &plan.IsActive, &plan.Advertise, &plan.IsAdvertised, &plan.CreatedAt)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "plan not found",
		})
	}

	if err := loadPlanExtras(ctx, h.db, &plan); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to load plan pricing",
		})
	}

	return c.JSON(plan)
}

// Update updates an existing plan (partial update).
// @Summary Update plan
// @Description Partially updates an existing plan
// @Tags admin-plans
// @Accept json
// @Produce json
// @Param id path string true "Plan ID"
// @Param body body updatePlanRequest true "Fields to update"
// @Success 200 {object} planResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans/{id} [put]
func (h *AdminPlanHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")

	var req updatePlanRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Required scalar fields cannot be cleared with null. Optional limits can.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(c.Body(), &fields); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if _, ok := fields["id"]; ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id is immutable"})
	}
	for _, field := range []string{"name", "duration_days", "max_devices", "node_group_id", "is_active", "advertise", "is_advertised"} {
		if raw, ok := fields[field]; ok && strings.TrimSpace(string(raw)) == "null" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": field + " must not be null"})
		}
	}
	if err := validatePlanInput(req.Name, req.TrafficLimit.Value, req.DurationDays, req.MaxDevices, req.MaxConcurrent.Value, req.SpeedLimit.Value, req.NodeGroupID, req.Prices, req.Features); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	// Build dynamic update query
	setClauses := ""
	args := []interface{}{}
	argIdx := 1

	if req.Name != nil {
		setClauses += comma(setClauses) + "name = $" + itoa(argIdx)
		args = append(args, *req.Name)
		argIdx++
	}
	if req.TrafficLimit.Present {
		setClauses += comma(setClauses) + "traffic_limit = $" + itoa(argIdx)
		args = append(args, req.TrafficLimit.Value)
		argIdx++
	}
	if req.DurationDays != nil {
		setClauses += comma(setClauses) + "duration_days = $" + itoa(argIdx)
		args = append(args, *req.DurationDays)
		argIdx++
	}
	if req.MaxConcurrent.Present {
		setClauses += comma(setClauses) + "max_concurrent = $" + itoa(argIdx)
		args = append(args, req.MaxConcurrent.Value)
		argIdx++
	}
	if req.MaxDevices != nil {
		setClauses += comma(setClauses) + "max_devices = $" + itoa(argIdx)
		args = append(args, *req.MaxDevices)
		argIdx++
	}
	if req.SpeedLimit.Present {
		setClauses += comma(setClauses) + "speed_limit = $" + itoa(argIdx)
		args = append(args, req.SpeedLimit.Value)
		argIdx++
	}
	if req.NodeGroupID != nil {
		setClauses += comma(setClauses) + "node_group_id = $" + itoa(argIdx)
		args = append(args, *req.NodeGroupID)
		argIdx++
	}
	if req.IsActive != nil {
		setClauses += comma(setClauses) + "is_active = $" + itoa(argIdx)
		args = append(args, *req.IsActive)
		argIdx++
	}

	if req.Advertise != nil {
		setClauses += comma(setClauses) + "advertise = $" + itoa(argIdx)
		args = append(args, *req.Advertise)
		argIdx++
	}

	// A pricing- or feature-only edit has no plan-column SET clause, so the
	// guard below only rejects a request with none of those either.
	if setClauses == "" && req.Prices == nil && req.Features == nil && req.IsAdvertised == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "no fields to update",
		})
	}

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update plan",
		})
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize all plan edits, including price-only changes and publication.
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM plans WHERE id = $1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "plan not found"})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update plan"})
	}

	if setClauses != "" {
		args = append(args, id)
		query := "UPDATE plans SET " + setClauses + " WHERE id = $" + itoa(argIdx)
		result, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update plan",
			})
		}
		if result.RowsAffected() == 0 {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "plan not found",
			})
		}
	} else {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = $1)`, id).Scan(&exists); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update plan",
			})
		}
		if !exists {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "plan not found",
			})
		}
	}

	if req.Prices != nil {
		if err := replacePlanPrices(ctx, tx, id, req.Prices); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update plan prices",
			})
		}
	}
	if req.Features != nil {
		if err := replacePlanFeatures(ctx, tx, id, req.Features); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to update plan features",
			})
		}
	}

	var active, advertise, hasPrice bool
	if err := tx.QueryRow(ctx, `SELECT is_active, advertise,
		EXISTS(SELECT 1 FROM plan_prices WHERE plan_id = plans.id AND duration_days > 0 AND price_cents >= 0)
		FROM plans WHERE id = $1`, id).Scan(&active, &advertise, &hasPrice); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update plan"})
	}
	if req.IsAdvertised != nil && *req.IsAdvertised && (!active || !advertise || !hasPrice) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "publication requires an active plan, advertise intent, and at least one valid price"})
	}
	// Never auto-publish. Removing any publication prerequisite withdraws it.
	if _, err := tx.Exec(ctx, `UPDATE plans SET is_advertised =
		CASE WHEN NOT $2 OR NOT $3 OR NOT $4 THEN false ELSE COALESCE($5::boolean, is_advertised) END
		WHERE id = $1`, id, active, advertise, hasPrice, req.IsAdvertised); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update plan"})
	}

	if err := tx.Commit(ctx); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update plan",
		})
	}

	return h.Get(c)
}

// Delete soft-deletes a plan by setting is_active to false.
// @Summary Delete plan
// @Description Soft-deletes a plan by deactivating it
// @Tags admin-plans
// @Produce json
// @Param id path string true "Plan ID"
// @Success 200 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /admin/plans/{id} [delete]
func (h *AdminPlanHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")

	result, err := h.db.Exec(
		context.Background(),
		`UPDATE plans SET is_active = false, is_advertised = false WHERE id = $1`,
		id,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete plan",
		})
	}

	if result.RowsAffected() == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "plan not found",
		})
	}

	return c.JSON(fiber.Map{"message": "plan deactivated"})
}

// comma returns ", " if s is non-empty, otherwise "".
func comma(s string) string {
	if s == "" {
		return ""
	}
	return ", "
}

// itoa converts an int to its string representation.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
