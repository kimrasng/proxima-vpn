package payments

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v86"
)

func TestExpiredTrialAttemptReconcilesStripeBeforeReplacement(t *testing.T) {
	for _, scenario := range []string{"existing", "absent", "provider_error"} {
		t.Run(scenario, func(t *testing.T) {
			var pages, creates int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/checkout/sessions" {
					creates++
					http.Error(w, "unexpected create", http.StatusBadRequest)
					return
				}
				pages++
				if r.URL.Query().Get("created[gte]") == "" || r.URL.Query().Get("created[lte]") == "" {
					t.Error("reconciliation did not bound the original creation window")
				}
				w.Header().Set("Content-Type", "application/json")
				switch scenario {
				case "provider_error":
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"unavailable"}}`))
				case "absent":
					_, _ = w.Write([]byte(`{"object":"list","data":[],"has_more":false}`))
				case "existing":
					if pages == 1 {
						_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"cs_other","metadata":{}}],"has_more":true}`))
						return
					}
					if r.URL.Query().Get("starting_after") != "cs_other" {
						t.Error("Stripe reconciliation skipped pagination")
					}
					_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"cs_existing","url":"https://checkout.stripe.test/existing","metadata":{"trial_subscription_id":"local-attempt"}}],"has_more":false}`)
				}
			}))
			defer server.Close()
			backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
				URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
			})
			provider := &StripeProvider{
				client: stripe.NewClient("sk_test_fixture", stripe.WithBackends(&stripe.Backends{API: backend})),
			}
			// Given an attempt whose original expiry is definitely in the past.
			req := TrialCheckoutRequest{ID: "local-attempt", ExpiresAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
			// When recovery queries Stripe, never create a second remote session.
			handoff, err := provider.StartTrial(context.Background(), req)
			if creates != 0 {
				t.Fatal("expired attempt blindly created a second checkout")
			}
			switch scenario {
			case "existing":
				if err != nil || handoff == nil || handoff.SessionID != "cs_existing" || pages != 2 {
					t.Fatalf("existing session was lost: %+v %v pages=%d", handoff, err, pages)
				}
			case "absent":
				if !errors.Is(err, ErrTrialCheckoutAbsent) {
					t.Fatalf("remote absence did not permit safe replacement: %v", err)
				}
			case "provider_error":
				if err == nil || errors.Is(err, ErrTrialCheckoutAbsent) {
					t.Fatalf("Stripe failure was mistaken for absence: %v", err)
				}
			}
		})
	}
}
