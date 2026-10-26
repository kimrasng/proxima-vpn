package payments

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/stripe/stripe-go/v86"
)

// TrialCheckoutRequest contains the local subscription's persisted price snapshot.
type TrialCheckoutRequest struct {
	ID           string
	UserEmail    string
	Currency     string
	PriceCents   int64
	DurationDays int
	TrialDays    int
	ExpiresAt    time.Time
}

// TrialGateway keeps subscription billing separate from one-time orders.
type TrialGateway interface {
	StartTrial(context.Context, TrialCheckoutRequest) (*CheckoutHandoff, error)
	CancelTrial(context.Context, string) error
	ReadTrial(context.Context, string) (*TrialSubscriptionState, error)
	// ReadPaymentMethod returns the default card, or a card attached to its customer
	// when no default is configured. It does not change billing defaults.
	ReadPaymentMethod(context.Context, string) (*PaymentMethodSummary, error)
	ReadTrialCheckout(context.Context, string) (*TrialEvent, error)
	TrialCurrency() string
}

var ErrTrialCheckoutAbsent = errors.New("expired trial attempt has no live Stripe checkout")

func (p *StripeProvider) TrialCurrency() string { return p.currency }

func (p *StripeProvider) ReadTrialCheckout(ctx context.Context, sessionID string) (*TrialEvent, error) {
	session, err := p.client.V1CheckoutSessions.Retrieve(ctx, sessionID, &stripe.CheckoutSessionRetrieveParams{})
	if err != nil {
		return nil, fmt.Errorf("retrieve trial checkout: %w", err)
	}
	kind := "checkout.session.completed"
	switch session.Status {
	case stripe.CheckoutSessionStatusExpired:
		kind = "checkout.session.expired"
	case stripe.CheckoutSessionStatusOpen:
		kind = "checkout.session.open"
	}
	return trialEventFromSession(session, "session:"+session.ID, kind)
}

type TrialSubscriptionState struct {
	ID           string
	LocalID      string
	CustomerID   string
	Status       string
	TrialEnd     int64
	PeriodEnd    int64
	PriceCents   int64
	Currency     string
	HasCard      bool
	CancelAtEnd  bool
	TrialStart   int64
	DurationDays int64
	Quantity     int64
	ItemID       string
}

// PaymentMethodSummary is the non-sensitive part of a saved card. Card numbers
// never reach this process; Stripe only exposes brand, last four and expiry.
type PaymentMethodSummary struct {
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int64  `json:"exp_month"`
	ExpYear  int64  `json:"exp_year"`
	Funding  string `json:"funding"`
	Wallet   string `json:"wallet,omitempty"`
}

func (p *StripeProvider) ReadPaymentMethod(ctx context.Context, subscriptionID string) (*PaymentMethodSummary, error) {
	params := &stripe.SubscriptionRetrieveParams{}
	params.AddExpand("default_payment_method")
	params.AddExpand("customer.invoice_settings.default_payment_method")
	subscription, err := p.client.V1Subscriptions.Retrieve(ctx, subscriptionID, params)
	if err != nil {
		return nil, fmt.Errorf("retrieve subscription payment method: %w", err)
	}
	method := subscription.DefaultPaymentMethod
	if method == nil && subscription.Customer != nil && subscription.Customer.InvoiceSettings != nil {
		method = subscription.Customer.InvoiceSettings.DefaultPaymentMethod
	}
	if method == nil && subscription.Customer != nil {
		listParams := &stripe.PaymentMethodListParams{
			Customer: stripe.String(subscription.Customer.ID),
			Type:     stripe.String(string(stripe.PaymentMethodTypeCard)),
		}
		listParams.Limit = stripe.Int64(1)
		for attached, err := range p.client.V1PaymentMethods.List(ctx, listParams).All(ctx) {
			if err != nil {
				return nil, fmt.Errorf("list customer payment methods: %w", err)
			}
			method = attached
			break
		}
	}
	if method == nil || method.Card == nil {
		return nil, nil
	}
	summary := &PaymentMethodSummary{
		Brand: string(method.Card.Brand), Last4: method.Card.Last4,
		ExpMonth: method.Card.ExpMonth, ExpYear: method.Card.ExpYear, Funding: string(method.Card.Funding),
	}
	if method.Card.Wallet != nil {
		summary.Wallet = string(method.Card.Wallet.Type)
	}
	return summary, nil
}

func (p *StripeProvider) ReadTrial(ctx context.Context, subscriptionID string) (*TrialSubscriptionState, error) {
	params := &stripe.SubscriptionRetrieveParams{}
	params.AddExpand("default_payment_method")
	subscription, err := p.client.V1Subscriptions.Retrieve(ctx, subscriptionID, params)
	if err != nil {
		return nil, fmt.Errorf("retrieve trial subscription: %w", err)
	}
	state := &TrialSubscriptionState{
		ID: subscription.ID, LocalID: subscription.Metadata["trial_subscription_id"],
		Status: string(subscription.Status), TrialEnd: subscription.TrialEnd,
		CancelAtEnd: subscription.CancelAtPeriodEnd,
		HasCard:     subscription.DefaultPaymentMethod != nil,
		TrialStart:  subscription.TrialStart,
	}
	if subscription.Customer != nil {
		state.CustomerID = subscription.Customer.ID
	}
	if subscription.Items != nil && len(subscription.Items.Data) == 1 {
		item := subscription.Items.Data[0]
		state.Quantity, state.ItemID = item.Quantity, item.ID
		state.PeriodEnd = item.CurrentPeriodEnd
		if item.Price != nil {
			state.PriceCents = item.Price.UnitAmount
			state.Currency = string(item.Price.Currency)
			if item.Price.Recurring != nil && item.Price.Recurring.Interval == "day" {
				state.DurationDays = item.Price.Recurring.IntervalCount
			}
		}
	}
	return state, nil
}

func (p *StripeProvider) trialParams(req TrialCheckoutRequest) (*stripe.CheckoutSessionCreateParams, error) {
	// Stripe day intervals support the panel's existing fixed-day price terms.
	if req.DurationDays < 1 || req.DurationDays > 365 || req.TrialDays < 1 || req.TrialDays > 730 || req.PriceCents <= 0 {
		return nil, fmt.Errorf("invalid trial billing terms")
	}
	data := p.priceData(req.PriceCents)
	data.Currency = stripe.String(req.Currency)
	data.Recurring = &stripe.CheckoutSessionCreateLineItemPriceDataRecurringParams{
		Interval: stripe.String("day"), IntervalCount: stripe.Int64(int64(req.DurationDays)),
	}
	metadata := map[string]string{"trial_subscription_id": req.ID}
	return &stripe.CheckoutSessionCreateParams{
		Mode: stripe.String("subscription"),
		SuccessURL: stripe.String(p.panelURL + "/portal/plan?trial=success&trial_id=" +
			url.QueryEscape(req.ID) + "&session_id={CHECKOUT_SESSION_ID}"),
		CancelURL:               stripe.String(p.panelURL + "/portal/plan?trial=cancelled"),
		CustomerEmail:           stripe.String(req.UserEmail),
		PaymentMethodCollection: stripe.String("always"),
		PaymentMethodTypes:      []*string{stripe.String("card")},
		ExpiresAt:               stripe.Int64(req.ExpiresAt.Unix()),
		Metadata:                metadata,
		ClientReferenceID:       stripe.String(req.ID),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{Quantity: stripe.Int64(1), PriceData: data},
		},
		SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{
			TrialPeriodDays: stripe.Int64(int64(req.TrialDays)), Metadata: metadata,
		},
	}, nil
}

func (p *StripeProvider) StartTrial(ctx context.Context, req TrialCheckoutRequest) (*CheckoutHandoff, error) {
	if !req.ExpiresAt.After(time.Now()) {
		// A process may have died before the HTTP request, or after Stripe
		// created the session. Search the original creation window before
		// replacing its expired idempotency key; never infer absence locally.
		list := p.client.V1CheckoutSessions.List(ctx, &stripe.CheckoutSessionListParams{
			CreatedRange: &stripe.RangeQueryParams{
				GreaterThanOrEqual: req.ExpiresAt.Add(-time.Hour - 5*time.Minute).Unix(),
				LesserThanOrEqual:  req.ExpiresAt.Unix(),
			},
		})
		for session, err := range list.All(ctx) {
			if err != nil {
				return nil, fmt.Errorf("reconcile uncertain trial checkout: %w", err)
			}
			if session.Metadata["trial_subscription_id"] == req.ID {
				return &CheckoutHandoff{Mode: ModeRedirect, RedirectURL: session.URL, SessionID: session.ID}, nil
			}
		}
		return nil, ErrTrialCheckoutAbsent
	}
	params, err := p.trialParams(req)
	if err != nil {
		return nil, err
	}
	params.SetIdempotencyKey("trial-checkout:" + req.ID)
	session, err := p.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("create trial checkout: %w", err)
	}
	return &CheckoutHandoff{Mode: ModeRedirect, RedirectURL: session.URL, SessionID: session.ID}, nil
}

// CancelTrial preserves the trial or paid period while disabling future billing.
func (p *StripeProvider) CancelTrial(ctx context.Context, subscriptionID string) error {
	_, err := p.client.V1Subscriptions.Update(ctx, subscriptionID, &stripe.SubscriptionUpdateParams{
		CancelAtPeriodEnd: stripe.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("schedule subscription cancellation: %w", err)
	}
	return nil
}
