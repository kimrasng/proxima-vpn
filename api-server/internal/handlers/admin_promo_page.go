package handlers

import (
	"errors"
	"net/mail"
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

type AdminPromoPageHandler struct{ db *pgxpool.Pool }

func NewAdminPromoPageHandler(db *pgxpool.Pool) *AdminPromoPageHandler {
	return &AdminPromoPageHandler{db: db}
}

type promoPageBody struct {
	services.PromoRules
	Headline    string  `json:"headline"`
	Description string  `json:"description"`
	CTALabel    string  `json:"cta_label"`
	PlanID      *string `json:"plan_id"`
}

func (h *AdminPromoPageHandler) Get(c *fiber.Ctx) error {
	var p promoPageBody
	err := h.db.QueryRow(c.UserContext(),
		`SELECT headline, description, cta_label, plan_id::text, countries,browsers,ip_cidrs,email_domains FROM promo_landing_pages WHERE code = $1`,
		strings.TrimSpace(c.Params("code")),
	).Scan(&p.Headline, &p.Description, &p.CTALabel, &p.PlanID, &p.Countries, &p.Browsers, &p.IPCIDRs, &p.EmailDomains)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.JSON(promoPageBody{})
	}
	if err != nil {
		return promotionKindError(c, err)
	}
	return c.JSON(p)
}

func (h *AdminPromoPageHandler) Put(c *fiber.Ctx) error {
	code := strings.TrimSpace(c.Params("code"))
	var p promoPageBody
	if err := c.BodyParser(&p); err != nil || code == "" {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request body"})
	}
	p.Headline, p.Description, p.CTALabel = strings.TrimSpace(p.Headline), strings.TrimSpace(p.Description), strings.TrimSpace(p.CTALabel)
	if utf8.RuneCountInString(p.Headline) > 80 || utf8.RuneCountInString(p.Description) > 300 || utf8.RuneCountInString(p.CTALabel) > 30 {
		return c.Status(400).JSON(fiber.Map{"error": "text too long"})
	}
	if p.PlanID != nil && *p.PlanID == "" {
		p.PlanID = nil
	}
	if p.PlanID != nil {
		if _, err := uuid.Parse(*p.PlanID); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid plan_id"})
		}
	}
	if err := normalizePromoRules(&p.PromoRules); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var exists bool
	if err := h.db.QueryRow(c.UserContext(), `SELECT
		EXISTS(SELECT 1 FROM promotion_codes WHERE code=$1)
		OR EXISTS(SELECT 1 FROM free_trial_promotions WHERE code=$1)
		OR EXISTS(SELECT 1 FROM affiliate_partners WHERE code=$1)`, code).Scan(&exists); err != nil {
		return promotionKindError(c, err)
	}
	if !exists {
		return c.SendStatus(404)
	}
	if _, err := h.db.Exec(c.UserContext(),
		`INSERT INTO promo_landing_pages (code, headline, description, cta_label, plan_id,countries,browsers,ip_cidrs,email_domains)
		 VALUES ($1, $2, $3, $4, $5,$6,$7,$8,$9)
		 ON CONFLICT (code) DO UPDATE SET headline = EXCLUDED.headline, description = EXCLUDED.description,
		   cta_label = EXCLUDED.cta_label, plan_id = EXCLUDED.plan_id,
		   countries=EXCLUDED.countries,browsers=EXCLUDED.browsers,ip_cidrs=EXCLUDED.ip_cidrs,email_domains=EXCLUDED.email_domains,updated_at = NOW()`,
		code, p.Headline, p.Description, p.CTALabel, p.PlanID, p.Countries, p.Browsers, p.IPCIDRs, p.EmailDomains,
	); err != nil {
		return promotionKindError(c, err)
	}
	return c.JSON(p)
}

func normalizePromoRules(r *services.PromoRules) error {
	for _, list := range []*[]string{&r.Countries, &r.Browsers, &r.IPCIDRs, &r.EmailDomains} {
		if len(*list) > 100 {
			return errors.New("too many eligibility rules")
		}
		if *list == nil {
			*list = []string{}
		}
		for i, value := range *list {
			(*list)[i] = strings.ToLower(strings.TrimSpace(value))
		}
	}
	for i, country := range r.Countries {
		country = strings.ToUpper(country)
		if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
			return errors.New("countries must be two-letter country codes")
		}
		r.Countries[i] = country
	}
	for _, browser := range r.Browsers {
		switch browser {
		case "chrome", "safari", "firefox", "edge", "unknown":
		default:
			return errors.New("unsupported browser")
		}
	}
	for i, value := range r.IPCIDRs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			ip, ipErr := netip.ParseAddr(value)
			if ipErr != nil {
				return errors.New("invalid IP or CIDR")
			}
			prefix = netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen())
		}
		r.IPCIDRs[i] = prefix.Masked().String()
	}
	for _, domain := range r.EmailDomains {
		address, err := mail.ParseAddress("user@" + domain)
		if err != nil || address.Address != "user@"+domain || !strings.Contains(domain, ".") {
			return errors.New("invalid email domain")
		}
	}
	return nil
}
