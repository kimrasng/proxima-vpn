package payments

import (
	"context"
	"fmt"

	"github.com/stripe/stripe-go/v86"
)

// BillingPortalProvider opens the billing portal for an owned Stripe customer.
type BillingPortalProvider interface {
	CreateBillingPortal(ctx context.Context, customerID string) (string, error)
}

func (p *StripeProvider) CreateBillingPortal(ctx context.Context, customerID string) (string, error) {
	returnURL := p.panelURL + "/portal/plan?billing=updated"
	session, err := p.client.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", fmt.Errorf("create billing portal session: %w", err)
	}
	return session.URL, nil
}
