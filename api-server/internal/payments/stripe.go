package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
)

// StripeProvider is the hosted checkout provider: StartCheckout creates a
// Checkout Session and hands back its URL for a full-page redirect;
// VerifyWebhook authenticates Stripe's asynchronous confirmation.
type StripeProvider struct {
	client        *stripe.Client
	webhookSecret string
	panelURL      string
	currency      string
	productID     string
}

// NewStripeProvider constructs a StripeProvider. Callers must check
// cfg.SecretKey/panelURL themselves before registering it (see Build) - this
// constructor does not validate, so a provider built from empty values would
// fail lazily on first use instead of at startup.
func NewStripeProvider(cfg config.StripeConfig, panelURL, currency string) *StripeProvider {
	if currency == "" {
		currency = "usd"
	}
	return &StripeProvider{
		client:        stripe.NewClient(cfg.SecretKey),
		webhookSecret: cfg.WebhookSecret,
		panelURL:      panelURL,
		currency:      currency,
		productID:     cfg.ProductID,
	}
}

func (p *StripeProvider) Name() string {
	return ProviderStripe
}

// StartCheckout creates a Checkout Session priced from req's own snapshot -
// price_data is built inline rather than referencing a pre-created Stripe
// Price object, since prices live in our plan_prices table, not Stripe's.
// Metadata carries the order id both on the session and on the resulting
// PaymentIntent, so the webhook (keyed on session/PaymentIntent) and any
// later refund/dispute event can both resolve back to the order.
func (p *StripeProvider) StartCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutHandoff, error) {
	successURL := p.panelURL + "/portal/plan?checkout=success&session_id={CHECKOUT_SESSION_ID}"
	cancelURL := p.panelURL + "/portal/plan?checkout=cancelled"

	params := &stripe.CheckoutSessionCreateParams{
		Mode:          stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL:    stripe.String(successURL),
		CancelURL:     stripe.String(cancelURL),
		CustomerEmail: stripe.String(req.UserEmail),
		ExpiresAt:     stripe.Int64(req.ExpiresAt.Unix()),
		Metadata: map[string]string{
			"order_id": req.OrderID,
		},
		PaymentIntentData: &stripe.CheckoutSessionCreatePaymentIntentDataParams{
			Metadata: map[string]string{
				"order_id": req.OrderID,
			},
		},
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{
				Quantity: stripe.Int64(1),
				// The Stripe product is a generic balance descriptor; never label the
				// item as a VPN service on Stripe.
				PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
					Currency:   stripe.String(p.currency),
					UnitAmount: stripe.Int64(req.PriceCents),
					Product:    stripe.String(p.productID),
				},
			},
		},
	}

	session, err := p.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("create checkout session: %w", err)
	}

	return &CheckoutHandoff{
		Mode:        ModeRedirect,
		RedirectURL: session.URL,
		SessionID:   session.ID,
	}, nil
}

// VerifyWebhook authenticates rawBody against the Stripe-Signature header
// and translates a paid checkout into a Confirmation. Every other event type,
// and a session whose PaymentStatus is not yet "paid" (an async payment
// method still settling - the follow-up arrives later as
// checkout.session.async_payment_succeeded), returns (nil, nil): authentic,
// but nothing for Settle to act on yet.
func (p *StripeProvider) VerifyWebhook(rawBody []byte, header func(string) string) (*Confirmation, error) {
	event, err := webhook.ConstructEvent(rawBody, header("Stripe-Signature"), p.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("verify signature: %w", err)
	}

	switch event.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
	default:
		return nil, nil
	}

	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return nil, fmt.Errorf("unmarshal checkout session: %w", err)
	}
	paid := session.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid
	// A free plan completes with nothing to collect; only a zero total counts.
	freeSession := session.PaymentStatus == stripe.CheckoutSessionPaymentStatusNoPaymentRequired && session.AmountTotal == 0
	if !paid && !freeSession {
		return nil, nil
	}
	customerEmail := strings.TrimSpace(session.CustomerEmail)
	if customerEmail == "" {
		customerEmail = strings.TrimSpace(session.CustomerDetails.Email)
	}
	if customerEmail == "" || session.CustomerDetails.Email != "" && !strings.EqualFold(session.CustomerDetails.Email, customerEmail) {
		return nil, fmt.Errorf("checkout session %s customer email is missing or inconsistent", session.ID)
	}

	orderID := session.Metadata["order_id"]
	if orderID == "" {
		return nil, fmt.Errorf("checkout session %s has no order_id in metadata", session.ID)
	}

	return &Confirmation{
		Provider:    ProviderStripe,
		ExternalID:  event.ID,
		OrderID:     orderID,
		AmountCents: session.AmountTotal,
		Currency:    string(session.Currency),
		SessionID:   session.ID,
		Payload:     rawBody,
		ActorType:   "provider",
		ActorID:     ProviderStripe,
		ActorLabel:  customerEmail,
	}, nil
}

// ReceiptURL returns the Stripe-hosted receipt of the charge behind a paid
// Checkout Session. Zero-total sessions never create a PaymentIntent, so they
// have no receipt and return "".
func (p *StripeProvider) ReceiptURL(ctx context.Context, sessionID string) (string, error) {
	params := &stripe.CheckoutSessionRetrieveParams{}
	params.AddExpand("payment_intent.latest_charge")
	session, err := p.client.V1CheckoutSessions.Retrieve(ctx, sessionID, params)
	if err != nil {
		return "", fmt.Errorf("retrieve checkout session: %w", err)
	}
	if session.PaymentIntent == nil || session.PaymentIntent.LatestCharge == nil {
		return "", nil
	}
	return session.PaymentIntent.LatestCharge.ReceiptURL, nil
}
