package payments

import (
	"testing"

	"github.com/stripe/stripe-go/v86"
)

func TestSessionDocumentURL(t *testing.T) {
	charge := &stripe.PaymentIntent{LatestCharge: &stripe.Charge{ReceiptURL: "https://pay.stripe.com/receipts/r"}}
	cases := []struct {
		name    string
		session *stripe.CheckoutSession
		want    string
	}{
		{"invoice wins", &stripe.CheckoutSession{Invoice: &stripe.Invoice{HostedInvoiceURL: "https://invoice.stripe.com/i/x"}, PaymentIntent: charge}, "https://invoice.stripe.com/i/x"},
		{"receipt when no invoice", &stripe.CheckoutSession{PaymentIntent: charge}, "https://pay.stripe.com/receipts/r"},
		{"receipt when invoice has no page", &stripe.CheckoutSession{Invoice: &stripe.Invoice{}, PaymentIntent: charge}, "https://pay.stripe.com/receipts/r"},
		{"zero total", &stripe.CheckoutSession{}, ""},
	}
	for _, tc := range cases {
		if got := sessionDocumentURL(tc.session); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
