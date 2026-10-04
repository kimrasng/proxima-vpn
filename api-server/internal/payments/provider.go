// Package payments defines the boundary between the order state machine and
// whatever actually moves money. Every payment provider (an administrator
// confirming a purchase by hand, Stripe's hosted checkout, or anything added
// later) implements Provider; adding a provider means writing one new file
// that implements the interfaces below and adding one line to Build - no
// change to the state machine, the handlers, or the webhook route.
package payments

import (
	"context"
	"time"
)

// CheckoutMode tells the caller what to do with a StartCheckout result.
type CheckoutMode string

const (
	// ModeRedirect: send the browser to CheckoutHandoff.RedirectURL.
	ModeRedirect CheckoutMode = "redirect"
	// ModeManual: there is nothing to redirect to; the order waits for an
	// out-of-band confirmation (an administrator marking it paid).
	ModeManual CheckoutMode = "manual"
)

const (
	ProviderAdmin  = "admin"
	ProviderStripe = "stripe"
)

// CheckoutRequest is the order, as a provider needs to see it. Every field is
// a snapshot already persisted on plan_orders - a provider never reads the
// database itself.
type CheckoutRequest struct {
	OrderID      string
	UserID       string
	UserEmail    string
	PlanName     string
	DurationDays int
	PriceCents   int64
	Currency     string
	// The order's own deadline. A hosted session's expiry is set to match it
	// so the panel and the provider never disagree about when it lapsed.
	ExpiresAt time.Time
}

// CheckoutHandoff is what starting a checkout hands back to the caller.
type CheckoutHandoff struct {
	Mode        CheckoutMode
	RedirectURL string // empty for ModeManual
	SessionID   string // provider's own handle for the attempt; may be empty
}

// Provider is what every payment provider implements.
type Provider interface {
	Name() string
	// StartCheckout runs after the order row already exists. It must not
	// mutate any panel state itself - it only talks to the provider (or, for
	// AdminProvider, talks to nothing at all).
	StartCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutHandoff, error)
}

// WebhookVerifier is implemented only by providers confirmed through an
// unauthenticated signed callback. AdminProvider deliberately does not
// implement it: its confirmation arrives on an admin-JWT route that is
// already authenticated, so there is no signature to verify and no untrusted
// body to parse.
type WebhookVerifier interface {
	// VerifyWebhook authenticates rawBody against the request's headers and
	// returns the confirmation it carries. (nil, nil) means "authentic but
	// not an event this provider's caller needs to act on" - the route
	// answers 200 and does nothing further. header is the request's
	// c.Get equivalent, not a Stripe-specific single header, so a future
	// provider reads whichever headers its own signature scheme needs.
	VerifyWebhook(rawBody []byte, header func(string) string) (*Confirmation, error)
}

// Confirmation is the single shape PaymentService.Settle consumes, whichever
// provider and whichever channel (webhook or authenticated route) produced it.
type Confirmation struct {
	Provider   string
	ExternalID string // idempotency key, unique within Provider
	OrderID    string

	// AmountCents/Currency are cross-checked against the order's own
	// snapshot when AmountCents > 0. Zero means "trusted channel, nothing
	// third-party to cross-check" - only legitimate for a provider whose
	// confirmation already arrived authenticated (the admin route).
	AmountCents int64
	Currency    string

	SessionID string // provider handle, kept on the audit row
	Payload   []byte // raw provider payload, stored as JSONB for replay review

	// Cheaply available request context, captured by the calling handler.
	RequestIP  string
	UserAgent  string
	RequestID  string
	ActorType  string // "admin" | "provider"
	ActorID    string
	ActorLabel string
}

// Registry is the set of providers wired in at startup, keyed by name.
type Registry map[string]Provider

func (r Registry) Get(name string) (Provider, bool) {
	p, ok := r[name]
	return p, ok
}

// Names returns the registered provider names, for the user-facing list of
// ways a plan can be paid for.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	return names
}
