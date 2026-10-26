package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/stripe/stripe-go/v86"
)

func TestCreateBillingPortalOpensOverviewForOwnedCustomer(t *testing.T) {
	var _ BillingPortalProvider = (*StripeProvider)(nil)
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			// Given
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodPost || r.URL.Path != "/v1/billing_portal/sessions" {
					t.Errorf("unexpected Stripe request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				want := url.Values{
					"customer":   {"cus_owned"},
					"return_url": {"https://panel.example/portal/plan?billing=updated"},
				}
				if !reflect.DeepEqual(r.PostForm, want) {
					t.Errorf("portal request = %v, want %v", r.PostForm, want)
				}
				w.WriteHeader(status)
				if status != http.StatusOK {
					_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"Stripe unavailable"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"bps_fixture","object":"billing_portal.session","url":"https://billing.stripe.com/p/session/fixture"}`))
			}))
			defer server.Close()
			backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
				URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
			})
			provider := &StripeProvider{
				client:   stripe.NewClient("sk_test_fixture", stripe.WithBackends(&stripe.Backends{API: backend})),
				panelURL: "https://panel.example",
			}
			// When
			got, err := provider.CreateBillingPortal(context.Background(), "cus_owned")
			// Then
			if calls.Load() != 1 {
				t.Fatalf("Stripe requests = %d, want only session creation", calls.Load())
			}
			if status != http.StatusOK {
				var stripeErr *stripe.Error
				if !errors.As(err, &stripeErr) || stripeErr.HTTPStatusCode != status || got != "" {
					t.Fatalf("URL = %q, error = %v, want wrapped Stripe status %d", got, err, status)
				}
				return
			}
			if err != nil || got != "https://billing.stripe.com/p/session/fixture" {
				t.Fatalf("URL = %q, error = %v", got, err)
			}
		})
	}
}
