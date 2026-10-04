package payments

import "context"

// AdminProvider is the confirmation channel where an administrator asserts,
// through an already-authenticated route, that an order was paid. It has no
// hosted checkout page - StartCheckout's ModeManual answer means the frontend
// shows "awaiting confirmation" rather than redirecting anywhere - and it
// implements no WebhookVerifier, since its confirmation never arrives as an
// unauthenticated callback.
type AdminProvider struct{}

func NewAdminProvider() *AdminProvider {
	return &AdminProvider{}
}

func (p *AdminProvider) Name() string {
	return ProviderAdmin
}

func (p *AdminProvider) StartCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutHandoff, error) {
	return &CheckoutHandoff{Mode: ModeManual}, nil
}
