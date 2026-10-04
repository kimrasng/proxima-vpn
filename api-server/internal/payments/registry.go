package payments

import (
	"log"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// Build is the one place a provider is wired in. Adding a provider is a new
// file implementing Provider (and, if confirmed asynchronously,
// WebhookVerifier) plus one line here - no handler, no route and no part of
// the order state machine changes.
func Build(cfg *config.Config) Registry {
	reg := Registry{
		ProviderAdmin: NewAdminProvider(),
	}

	if cfg.Payments.Stripe.SecretKey != "" && cfg.Server.PanelURL != "" {
		reg[ProviderStripe] = NewStripeProvider(cfg.Payments.Stripe, cfg.Server.PanelURL, cfg.Payments.Currency)
	} else {
		log.Println("payments: stripe not registered (STRIPE_SECRET_KEY or PANEL_URL unset)")
	}

	return reg
}
