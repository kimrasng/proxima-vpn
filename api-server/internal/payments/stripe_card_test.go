package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stripe/stripe-go/v86"
)

func TestReadPaymentMethodDefaultsAndAttachedCards(t *testing.T) {
	card := &stripe.PaymentMethod{
		ID: "pm_saved", Type: stripe.PaymentMethodTypeCard,
		Card: &stripe.PaymentMethodCard{
			Brand: stripe.PaymentMethodCardBrandVisa, Last4: "4242",
			ExpMonth: 5, ExpYear: 2029,
		},
	}
	for _, tc := range []struct {
		name          string
		subDefault    bool
		cusDefault    bool
		attached      bool
		listError     bool
		wantListCalls int
		wantCard      bool
	}{
		{name: "subscription default", subDefault: true, attached: true, wantCard: true},
		{name: "customer default", cusDefault: true, attached: true, wantCard: true},
		{name: "attached without default", attached: true, wantListCalls: 1, wantCard: true},
		{name: "no attached card", wantListCalls: 1},
		{name: "Stripe list failure", listError: true, wantListCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given Stripe defaults and customer-attached cards are separate resources.
			var listCalls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("card lookup must be read-only, got %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/subscriptions/sub_saved":
					if r.URL.Query().Get("expand[0]") != "default_payment_method" ||
						r.URL.Query().Get("expand[1]") != "customer.invoice_settings.default_payment_method" {
						t.Errorf("missing default expansions: %v", r.URL.Query())
					}
					sub := &stripe.Subscription{
						ID: "sub_saved", Status: stripe.SubscriptionStatusCanceled,
						Customer: &stripe.Customer{ID: "cus_saved", InvoiceSettings: &stripe.CustomerInvoiceSettings{}},
					}
					if tc.subDefault {
						sub.DefaultPaymentMethod = card
					}
					if tc.cusDefault {
						sub.Customer.InvoiceSettings.DefaultPaymentMethod = card
					}
					if err := json.NewEncoder(w).Encode(sub); err != nil {
						t.Error(err)
					}
				case "/v1/payment_methods":
					listCalls++
					if r.URL.Query().Get("customer") != "cus_saved" ||
						r.URL.Query().Get("type") != "card" || r.URL.Query().Get("limit") != "1" {
						t.Errorf("wrong customer card query: %v", r.URL.Query())
					}
					if tc.listError {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"error":{"message":"card lookup failed","type":"invalid_request_error"}}`))
						return
					}
					cards := []*stripe.PaymentMethod{}
					if tc.attached {
						cards = append(cards, card)
					}
					if err := json.NewEncoder(w).Encode(map[string]any{
						"object": "list", "data": cards, "has_more": false,
					}); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected Stripe path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
				URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
			})
			provider := &StripeProvider{
				client: stripe.NewClient("sk_test_fixture", stripe.WithBackends(&stripe.Backends{API: backend})),
			}

			// When the backend reads a saved card for a canceled subscription.
			got, err := provider.ReadPaymentMethod(context.Background(), "sub_saved")

			// Then defaults win, otherwise an attached card is read without changing Stripe.
			if (err != nil) != tc.listError {
				t.Fatalf("ReadPaymentMethod error = %v", err)
			}
			if listCalls != tc.wantListCalls || (got != nil) != tc.wantCard {
				t.Fatalf("card=%+v list calls=%d, want card=%v calls=%d",
					got, listCalls, tc.wantCard, tc.wantListCalls)
			}
			if got != nil && (got.Brand != "visa" || got.Last4 != "4242" || got.ExpMonth != 5 || got.ExpYear != 2029) {
				t.Fatalf("wrong card summary: %+v", got)
			}
		})
	}
}
