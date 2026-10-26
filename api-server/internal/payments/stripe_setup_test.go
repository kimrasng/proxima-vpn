package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
)

func TestCheckoutUsesPaymentForZeroTotalAndCampaignNameForDiscount(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		total      int64
	}{
		{"Welcome offer", "SECRET", 800},
		{"", "FALLBACK", 800},
		{"Free offer", "FREE", 0},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/coupons" {
					want := tc.name
					if want == "" {
						want = tc.code
					}
					if r.Form.Get("name") != want {
						t.Errorf("coupon label=%q, want %q", r.Form.Get("name"), want)
					}
					if r.Form.Get("amount_off") != "200" || r.Form.Get("currency") != "usd" ||
						r.Form.Get("duration") != "once" || r.Form.Get("max_redemptions") != "1" {
						t.Errorf("invalid coupon discount: %v", r.Form)
					}
					_, _ = w.Write([]byte(`{"id":"coupon_test","object":"coupon"}`))
					return
				}
				if r.URL.Path != "/v1/checkout/sessions" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if r.Form.Get("mode") != "payment" || r.Form.Get("discounts[0][coupon]") != "coupon_test" ||
					r.Form.Get("line_items[0][quantity]") != "1" ||
					r.Form.Get("line_items[0][price_data][unit_amount]") != strconv.FormatInt(tc.total+200, 10) ||
					r.Form.Get("line_items[0][price_data][currency]") != "usd" ||
					r.Form.Get("line_items[0][price_data][product_data][name]") != genericProductName {
					t.Errorf("invalid discounted params: %v", r.Form)
				}
				if r.Form.Get("adaptive_pricing[enabled]") != "true" || r.Form.Get("payment_method_types[0]") != "" {
					t.Error("localized checkout must allow Stripe to select eligible local methods")
				}
				if r.Form.Get("payment_method_collection") != "" || r.Form.Get("setup_intent_data[metadata][order_id]") != "" {
					t.Errorf("checkout must not require card registration: %v", r.Form)
				}
				_, _ = w.Write([]byte(`{"id":"cs_test","object":"checkout.session","url":"https://checkout.stripe.test/session"}`))
			}))
			defer server.Close()
			backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
				URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
			})
			provider := &StripeProvider{client: stripe.NewClient("sk_test_fixture", stripe.WithBackends(&stripe.Backends{API: backend})), currency: "usd"}
			handoff, err := provider.StartCheckout(context.Background(), CheckoutRequest{
				OrderID: "order-test", UserEmail: "qa@example.test", PriceCents: tc.total, DiscountCents: 200,
				PromotionCode: tc.code, PromotionName: tc.name, ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatal(err)
			}
			if handoff.Mode != ModeRedirect || handoff.SessionID != "cs_test" || handoff.RedirectURL != "https://checkout.stripe.test/session" {
				t.Fatalf("checkout handoff = %+v", handoff)
			}
			if calls != 2 {
				t.Fatalf("calls=%d, want 2", calls)
			}
		})
	}
}

func TestNoCostConfirmationFromStripeRetrievalAndWebhook(t *testing.T) {
	session := &stripe.CheckoutSession{
		ID: "cs_free", Mode: stripe.CheckoutSessionModePayment,
		Status: stripe.CheckoutSessionStatusComplete, PaymentStatus: stripe.CheckoutSessionPaymentStatusNoPaymentRequired,
		Currency: stripe.CurrencyUSD, CustomerEmail: "qa@example.test",
		Metadata: map[string]string{"order_id": "order-free"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/checkout/sessions/cs_free" || r.URL.RawQuery != "" {
			t.Errorf("unexpected retrieval: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(session); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
	})
	provider := &StripeProvider{
		client:        stripe.NewClient("sk_test_fixture", stripe.WithBackends(&stripe.Backends{API: backend})),
		webhookSecret: "whsec_test",
	}
	for range 2 {
		result, err := provider.ConfirmSession(context.Background(), session.ID)
		if err != nil || result == nil || result.ExternalID != "session:cs_free" ||
			result.SessionID != session.ID || result.OrderID != "order-free" || result.AmountCents != 0 {
			t.Fatalf("retrieved no-cost confirmation = %+v, %v", result, err)
		}
	}
	for _, status := range []stripe.CheckoutSessionStatus{stripe.CheckoutSessionStatusComplete, stripe.CheckoutSessionStatusOpen} {
		session.Status = status
		rawSession, err := json.Marshal(session)
		if err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"id":"evt_free","object":"event","api_version":"` + stripe.APIVersion + `","type":"checkout.session.completed","data":{"object":` + string(rawSession) + `}}`)
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: body, Secret: provider.webhookSecret})
		result, err := provider.VerifyWebhook(body, func(string) string { return signed.Header })
		if err != nil || (result != nil) != (status == stripe.CheckoutSessionStatusComplete) {
			t.Fatalf("webhook status %s confirmation = %+v, %v", status, result, err)
		}
		if result != nil && (result.ExternalID != "evt_free" || result.SessionID != "cs_free" || result.OrderID != "order-free" || result.AmountCents != 0) {
			t.Fatalf("webhook identity lost: %+v", result)
		}
	}
	result, err := provider.ConfirmSession(context.Background(), session.ID)
	if err != nil || result != nil {
		t.Fatalf("retrieved incomplete checkout accepted: %+v, %v", result, err)
	}
}

func TestPaymentConfirmationRequiresCompletedPaidOrNoCostSession(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mode          stripe.CheckoutSessionMode
		status        stripe.CheckoutSessionStatus
		paymentStatus stripe.CheckoutSessionPaymentStatus
		total         int64
		confirmed     bool
	}{
		{"free complete", stripe.CheckoutSessionModePayment, stripe.CheckoutSessionStatusComplete, stripe.CheckoutSessionPaymentStatusNoPaymentRequired, 0, true},
		{"free open", stripe.CheckoutSessionModePayment, stripe.CheckoutSessionStatusOpen, stripe.CheckoutSessionPaymentStatusNoPaymentRequired, 0, false},
		{"free expired", stripe.CheckoutSessionModePayment, stripe.CheckoutSessionStatusExpired, stripe.CheckoutSessionPaymentStatusNoPaymentRequired, 0, false},
		{"unpaid complete", stripe.CheckoutSessionModePayment, stripe.CheckoutSessionStatusComplete, stripe.CheckoutSessionPaymentStatusUnpaid, 0, false},
		{"nonzero without payment", stripe.CheckoutSessionModePayment, stripe.CheckoutSessionStatusComplete, stripe.CheckoutSessionPaymentStatusNoPaymentRequired, 200, false},
		{"paid complete", stripe.CheckoutSessionModePayment, stripe.CheckoutSessionStatusComplete, stripe.CheckoutSessionPaymentStatusPaid, 800, true},
		{"paid open", stripe.CheckoutSessionModePayment, stripe.CheckoutSessionStatusOpen, stripe.CheckoutSessionPaymentStatusPaid, 800, false},
		{"setup complete", stripe.CheckoutSessionModeSetup, stripe.CheckoutSessionStatusComplete, stripe.CheckoutSessionPaymentStatusNoPaymentRequired, 0, false},
		{"subscription complete", stripe.CheckoutSessionModeSubscription, stripe.CheckoutSessionStatusComplete, stripe.CheckoutSessionPaymentStatusPaid, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &stripe.CheckoutSession{
				ID: "cs_payment", Mode: tc.mode, Status: tc.status, PaymentStatus: tc.paymentStatus,
				AmountTotal: tc.total, Currency: stripe.CurrencyUSD, CustomerEmail: "qa@example.test",
				Metadata: map[string]string{"order_id": "order-1"},
			}
			result, err := confirmationFromSession(session, "event", []byte(`{"id":"cs_payment"}`))
			if err != nil {
				t.Fatal(err)
			}
			if (result != nil) != tc.confirmed {
				t.Fatalf("confirmation = %+v, want confirmed %v", result, tc.confirmed)
			}
			if result != nil && (result.OrderID != "order-1" || result.AmountCents != tc.total ||
				result.ExternalID != "event" || result.SessionID != session.ID || result.Currency != "usd" ||
				result.ActorLabel != "qa@example.test" || string(result.Payload) != `{"id":"cs_payment"}`) {
				t.Fatalf("confirmation identity or amount lost: %+v", result)
			}
		})
	}
}
