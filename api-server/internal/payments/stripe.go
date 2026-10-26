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

// genericProductName labels the line item when no pre-created Stripe product
// is configured. It stays a neutral balance descriptor for the same reason the
// configured product does: the item must never read as a VPN service.
const genericProductName = "Account balance"

// priceData references the configured Stripe product, or describes a generic
// one inline when STRIPE_PRODUCT_ID is unset - Stripe rejects an empty product
// id outright, which would fail every checkout.
func (p *StripeProvider) priceData(priceCents int64) *stripe.CheckoutSessionCreateLineItemPriceDataParams {
	data := &stripe.CheckoutSessionCreateLineItemPriceDataParams{
		Currency:   stripe.String(p.currency),
		UnitAmount: stripe.Int64(priceCents),
	}
	if p.productID != "" {
		data.Product = stripe.String(p.productID)
	} else {
		data.ProductData = &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
			Name: stripe.String(genericProductName),
		}
	}
	return data
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
// couponDisplayName labels the Stripe coupon for a promotion. Stripe caps
// the coupon name at 40 characters, while promotion codes are unbounded, so
// a long code is truncated (by runes, not bytes) rather than failing the
// checkout. The full code never leaves the panel: Stripe only shows this
// label, and the amount comes from the server-side discount snapshot.
func couponDisplayName(code string) string {
	const maxCouponNameRunes = 40
	if code == "" {
		return "Promotion"
	}
	runes := []rune(code)
	if len(runes) <= maxCouponNameRunes {
		return code
	}
	return string(runes[:maxCouponNameRunes])
}

func (p *StripeProvider) StartCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutHandoff, error) {
	successURL := p.panelURL + "/portal/plan?checkout=success&order_id=" + req.OrderID + "&session_id={CHECKOUT_SESSION_ID}"
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
		// A paid invoice (invoice.stripe.com, with PDF) instead of only the
		// charge receipt. Stripe bills this separately per paid session.
		InvoiceCreation: &stripe.CheckoutSessionCreateInvoiceCreationParams{
			Enabled: stripe.Bool(true),
			InvoiceData: &stripe.CheckoutSessionCreateInvoiceCreationInvoiceDataParams{
				Metadata: map[string]string{
					"order_id": req.OrderID,
				},
			},
		},
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{
				Quantity: stripe.Int64(1),
				// The Stripe product is a generic balance descriptor; never label the
				// item as a VPN service on Stripe.
				// When the order carries a promotion, the line item is the original
				// (pre-discount) price and a coupon shows the discount separately.
				PriceData: p.priceData(req.PriceCents + req.DiscountCents),
			},
		},
	}

	// When the order carries a promotion discount, create a one-time Stripe
	// coupon so Stripe's checkout page shows the original price with a
	// discount line rather than just the reduced total.
	if req.DiscountCents > 0 {
		couponName := couponDisplayName(req.PromotionCode)
		coupon, err := p.client.V1Coupons.Create(ctx, &stripe.CouponCreateParams{
			AmountOff:      stripe.Int64(req.DiscountCents),
			Currency:       stripe.String(p.currency),
			Duration:       stripe.String(string(stripe.CouponDurationOnce)),
			MaxRedemptions: stripe.Int64(1),
			Name:           stripe.String(couponName),
		})
		if err != nil {
			return nil, fmt.Errorf("create promotion coupon: %w", err)
		}
		params.Discounts = []*stripe.CheckoutSessionCreateDiscountParams{
			{Coupon: stripe.String(coupon.ID)},
		}
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
	return confirmationFromSession(&session, event.ID, rawBody)
}

// ConfirmSession reads a Checkout Session back from Stripe and settles it the
// same way the webhook would. The Stripe API, not the browser, is the source
// of truth: the caller only supplies an id, and everything the order is
// granted on (paid status, amount, order_id metadata) comes from Stripe.
// The session id doubles as the idempotency key, so a webhook arriving later
// for the same payment still finds the order already claimed and is ignored.
func (p *StripeProvider) ConfirmSession(ctx context.Context, sessionID string) (*Confirmation, error) {
	session, err := p.client.V1CheckoutSessions.Retrieve(ctx, sessionID, &stripe.CheckoutSessionRetrieveParams{})
	if err != nil {
		return nil, fmt.Errorf("retrieve checkout session: %w", err)
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return nil, fmt.Errorf("marshal checkout session: %w", err)
	}
	return confirmationFromSession(session, "session:"+session.ID, payload)
}

// confirmationFromSession applies the checks shared by the webhook and the
// return-from-checkout path. (nil, nil) means the session is authentic but
// not paid yet (an async method still settling).
func confirmationFromSession(session *stripe.CheckoutSession, externalID string, payload []byte) (*Confirmation, error) {
	paid := session.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid
	// A free plan completes with nothing to collect; only a zero total counts.
	freeSession := session.PaymentStatus == stripe.CheckoutSessionPaymentStatusNoPaymentRequired && session.AmountTotal == 0
	if !paid && !freeSession {
		return nil, nil
	}
	customerEmail := strings.TrimSpace(session.CustomerEmail)
	detailsEmail := ""
	if session.CustomerDetails != nil {
		detailsEmail = session.CustomerDetails.Email
	}
	if customerEmail == "" {
		customerEmail = strings.TrimSpace(detailsEmail)
	}
	if customerEmail == "" || detailsEmail != "" && !strings.EqualFold(detailsEmail, customerEmail) {
		return nil, fmt.Errorf("checkout session %s customer email is missing or inconsistent", session.ID)
	}

	orderID := session.Metadata["order_id"]
	if orderID == "" {
		return nil, fmt.Errorf("checkout session %s has no order_id in metadata", session.ID)
	}

	return &Confirmation{
		Provider:    ProviderStripe,
		ExternalID:  externalID,
		OrderID:     orderID,
		AmountCents: session.AmountTotal,
		Currency:    string(session.Currency),
		SessionID:   session.ID,
		Payload:     payload,
		ActorType:   "provider",
		ActorID:     ProviderStripe,
		ActorLabel:  customerEmail,
	}, nil
}

// ReceiptURL returns the Stripe-hosted document for a paid Checkout Session:
// the paid invoice page when the session created one, otherwise the charge
// receipt (sessions created before invoice creation was enabled). Zero-total
// sessions have neither and return "".
func (p *StripeProvider) ReceiptURL(ctx context.Context, sessionID string) (string, error) {
	params := &stripe.CheckoutSessionRetrieveParams{}
	params.AddExpand("invoice")
	params.AddExpand("payment_intent.latest_charge")
	session, err := p.client.V1CheckoutSessions.Retrieve(ctx, sessionID, params)
	if err != nil {
		return "", fmt.Errorf("retrieve checkout session: %w", err)
	}
	return sessionDocumentURL(session), nil
}

// sessionDocumentURL prefers the hosted invoice over the charge receipt.
func sessionDocumentURL(session *stripe.CheckoutSession) string {
	if session.Invoice != nil && session.Invoice.HostedInvoiceURL != "" {
		return session.Invoice.HostedInvoiceURL
	}
	if session.PaymentIntent != nil && session.PaymentIntent.LatestCharge != nil {
		return session.PaymentIntent.LatestCharge.ReceiptURL
	}
	return ""
}
