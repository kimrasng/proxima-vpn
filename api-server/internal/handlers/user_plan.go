package handlers

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

// UserPlanHandler handles user-facing plan browsing and node listing endpoints.
type UserPlanHandler struct {
	db       *pgxpool.Pool
	activity *services.ActivityService
}

// NewUserPlanHandler creates a new UserPlanHandler.
func NewUserPlanHandler(db *pgxpool.Pool) *UserPlanHandler {
	return &UserPlanHandler{db: db, activity: services.NewActivityService(db)}
}

type userPlanPrice struct {
	DurationDays int   `json:"duration_days"`
	PriceCents   int64 `json:"price_cents"`
}

type userPlanFeature struct {
	Text     string `json:"text"`
	Included bool   `json:"included"`
}

type userPlanItem struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	TrafficLimit *int64            `json:"traffic_limit"`
	DurationDays int               `json:"duration_days"`
	MaxDevices   int               `json:"max_devices"`
	SpeedLimit   *int              `json:"speed_limit"`
	Prices       []userPlanPrice   `json:"prices"`
	Features     []userPlanFeature `json:"features"`
	// Derived the same way as the admin side: a plan with no priced duration
	// cannot be ordered; operational activity, advertisement intent and final
	// publication approval separately gate visibility and new purchases.
	Purchasable bool `json:"purchasable"`
}

// userNodeItem is the minimal node view an end user gets: no address, port, key
// or protocol detail, since the subscription is the only place credentials
// belong. Anything added here is published to every user.
type userNodeItem struct {
	Name    string `json:"name"`
	Country string `json:"country"`
	Region  string `json:"region"`
	Status  string `json:"status"`
}

// ListNodes returns the nodes the authenticated user's plan grants access to.
// @Summary List my available nodes
// @Description Returns name and location of the nodes the user's plan can use
// @Tags user-plans
// @Produce json
// @Success 200 {array} userNodeItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/nodes [get]
func (h *UserPlanHandler) ListNodes(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)

	// The same plan -> node group -> node chain the Xray config is built from,
	// so this cannot advertise a node the user could not reach. No plan or no
	// group joins to nothing and yields [].
	rows, err := h.db.Query(
		context.Background(),
		`SELECT COALESCE(NULLIF(nl.name, ''), n.name), n.country, n.region, n.status
		 FROM nodes n
		 JOIN node_group_nodes ngn ON ngn.node_id = n.id
		 JOIN node_groups ng ON ng.id = ngn.node_group_id
		 JOIN plans p ON p.node_group_id = ng.id
		 JOIN users u ON u.plan_id = p.id
		 LEFT JOIN node_labels nl ON nl.node_id = n.id AND nl.language = u.language
		 WHERE u.id = $1 AND n.status <> 'pending'
		 ORDER BY n.country, n.name`,
		userID,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list nodes"})
	}
	defer rows.Close()

	items := make([]userNodeItem, 0)
	for rows.Next() {
		var n userNodeItem
		if err := rows.Scan(&n.Name, &n.Country, &n.Region, &n.Status); err != nil {
			continue
		}
		items = append(items, n)
	}

	return c.JSON(items)
}

// @Summary List available plans
// @Description Returns active subscription plans with approved advertisement
// @Tags user-plans
// @Produce json
// @Success 200 {array} userPlanItem
// @Failure 500 {object} map[string]string
// @Security BearerAuth
// @Router /user/plans [get]
func (h *UserPlanHandler) ListPlans(c *fiber.Ctx) error {
	ctx := context.Background()
	lang := h.resolveLanguage(c)

	rows, err := h.db.Query(ctx,
		`SELECT id, name, traffic_limit, duration_days, max_devices, speed_limit
		 FROM plans WHERE is_active = true AND advertise = true AND is_advertised = true ORDER BY name`,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
	}

	items := make([]userPlanItem, 0)
	ids := make([]string, 0)
	byID := make(map[string]*userPlanItem)
	for rows.Next() {
		var p userPlanItem
		if err := rows.Scan(&p.ID, &p.Name, &p.TrafficLimit, &p.DurationDays, &p.MaxDevices, &p.SpeedLimit); err != nil {
			rows.Close()
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
		}
		p.Prices = make([]userPlanPrice, 0)
		p.Features = make([]userPlanFeature, 0)
		items = append(items, p)
		ids = append(ids, p.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
	}
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	if len(ids) == 0 {
		return c.JSON(items)
	}

	priceRows, err := h.db.Query(ctx,
		`SELECT plan_id, duration_days, price_cents FROM plan_prices
		 WHERE plan_id = ANY($1) ORDER BY duration_days`,
		ids,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
	}
	for priceRows.Next() {
		var planID string
		var pr userPlanPrice
		if err := priceRows.Scan(&planID, &pr.DurationDays, &pr.PriceCents); err != nil {
			priceRows.Close()
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
		}
		if p, ok := byID[planID]; ok {
			p.Prices = append(p.Prices, pr)
			p.Purchasable = true
		}
	}
	priceRows.Close()
	if err := priceRows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
	}

	// text falls back to English when the resolved language has no bullet
	// authored for that position, and the bullet is dropped entirely if
	// neither language has text - an admin-incomplete bullet must not render
	// as an empty line.
	featureRows, err := h.db.Query(ctx,
		`SELECT pf.plan_id, pf.position, pf.included, COALESCE(t.text, en.text) AS text
		 FROM plan_features pf
		 LEFT JOIN plan_feature_texts t
		        ON t.plan_id = pf.plan_id AND t.position = pf.position AND t.language = $2
		 LEFT JOIN plan_feature_texts en
		        ON en.plan_id = pf.plan_id AND en.position = pf.position AND en.language = 'en'
		 WHERE pf.plan_id = ANY($1) AND COALESCE(t.text, en.text) IS NOT NULL
		 ORDER BY pf.plan_id, pf.position`,
		ids, lang,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
	}
	for featureRows.Next() {
		var planID string
		var pos int
		var f userPlanFeature
		if err := featureRows.Scan(&planID, &pos, &f.Included, &f.Text); err != nil {
			featureRows.Close()
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
		}
		if p, ok := byID[planID]; ok {
			p.Features = append(p.Features, f)
		}
	}
	featureRows.Close()
	if err := featureRows.Err(); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list plans"})
	}

	return c.JSON(items)
}

// resolveLanguage picks the language plan feature bullets render in: an
// explicit ?lang= query param (the web app's active i18next language) wins,
// then the user's stored profile preference, then English.
func (h *UserPlanHandler) resolveLanguage(c *fiber.Ctx) string {
	if q := c.Query("lang"); q != "" {
		return q
	}
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return "en"
	}
	var lang string
	if err := h.db.QueryRow(context.Background(),
		`SELECT language FROM users WHERE id = $1`, userID,
	).Scan(&lang); err != nil || lang == "" {
		return "en"
	}
	return lang
}
