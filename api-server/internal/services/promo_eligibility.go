package services

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

type PromoVisitor struct {
	Country string
	Browser string
	IP      string
	Email   string
}

type PromoRules struct {
	Countries    []string `json:"countries"`
	Browsers     []string `json:"browsers"`
	IPCIDRs      []string `json:"ip_cidrs"`
	EmailDomains []string `json:"email_domains"`
}

type promoQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func CheckPromoEligibility(ctx context.Context, db promoQuerier, code string, visitor PromoVisitor) error {
	var rules PromoRules
	err := db.QueryRow(ctx, `SELECT countries,browsers,ip_cidrs,email_domains
		FROM promo_landing_pages WHERE code=$1`, code).
		Scan(&rules.Countries, &rules.Browsers, &rules.IPCIDRs, &rules.EmailDomains)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load promotion eligibility: %w", err)
	}
	if !rules.Allows(visitor) {
		return &PromotionRejectError{Reason: PromotionRejectReason("not_eligible")}
	}
	return nil
}

func (r PromoRules) Allows(v PromoVisitor) bool {
	if len(r.Countries) > 0 && !slices.Contains(r.Countries, strings.ToUpper(v.Country)) {
		return false
	}
	if len(r.Browsers) > 0 && !slices.Contains(r.Browsers, strings.ToLower(v.Browser)) {
		return false
	}
	if len(r.IPCIDRs) > 0 {
		ip, err := netip.ParseAddr(v.IP)
		if err != nil {
			return false
		}
		matched := false
		for _, cidr := range r.IPCIDRs {
			prefix, err := netip.ParsePrefix(cidr)
			if err == nil && prefix.Contains(ip.Unmap()) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	// Anonymous visitors may see the offer; their account email is checked on application.
	if len(r.EmailDomains) > 0 && v.Email != "" {
		_, domain, ok := strings.Cut(strings.ToLower(v.Email), "@")
		if !ok || !slices.Contains(r.EmailDomains, domain) {
			return false
		}
	}
	return true
}
