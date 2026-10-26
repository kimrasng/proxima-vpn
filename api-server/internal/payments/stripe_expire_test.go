package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stripe/stripe-go/v86"
)

func TestExpireCheckoutHandlesRemoteStateAndCompletionRace(t *testing.T) {
	var _ CheckoutExpirer = (*StripeProvider)(nil)
	for _, tc := range []struct {
		name          string
		initial       string
		raced         string
		expireError   int
		readError     int
		wantComplete  bool
		wantError     bool
		wantReadbacks int
	}{
		{name: "open", initial: "open"},
		{name: "already expired", initial: "expired", wantReadbacks: 1},
		{name: "already completed", initial: "complete", wantComplete: true, wantError: true, wantReadbacks: 1},
		{name: "completion races expiration", initial: "open", raced: "complete", wantComplete: true, wantError: true, wantReadbacks: 1},
		{name: "expiration races expiration", initial: "open", raced: "expired", wantReadbacks: 1},
		{name: "Stripe unavailable", initial: "open", expireError: http.StatusInternalServerError, wantError: true},
		{name: "readback fails", initial: "complete", readError: http.StatusInternalServerError, wantError: true, wantReadbacks: 1},
		{name: "unrelated invalid request", initial: "open", expireError: http.StatusBadRequest, wantError: true, wantReadbacks: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := tc.initial
			var expires, readbacks int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions/cs_cancel/expire":
					expires++
					if tc.raced != "" {
						state = tc.raced
					}
					code := tc.expireError
					if state != "open" && code == 0 {
						code = http.StatusBadRequest
					}
					if code != 0 {
						w.WriteHeader(code)
						_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"Cannot expire session"}}`))
						return
					}
					state = "expired"
					_, _ = w.Write([]byte(`{"id":"cs_cancel","object":"checkout.session","status":"expired"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/cs_cancel":
					readbacks++
					if tc.readError != 0 {
						w.WriteHeader(tc.readError)
						_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"Unavailable"}}`))
						return
					}
					_, _ = w.Write([]byte(`{"id":"cs_cancel","object":"checkout.session","status":"` + state + `"}`))
				default:
					t.Errorf("unexpected Stripe request: %s %s", r.Method, r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
				URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
			})
			provider := &StripeProvider{
				client: stripe.NewClient("sk_test_fixture", stripe.WithBackends(&stripe.Backends{API: backend})),
			}
			err := provider.ExpireCheckout(context.Background(), "cs_cancel")
			if (err != nil) != tc.wantError || errors.Is(err, ErrCheckoutComplete) != tc.wantComplete {
				t.Fatalf("expiration error = %v, want error %v, complete %v", err, tc.wantError, tc.wantComplete)
			}
			if expires != 1 || readbacks != tc.wantReadbacks {
				t.Fatalf("requests: expire=%d readback=%d, want 1/%d", expires, readbacks, tc.wantReadbacks)
			}
			if !tc.wantError {
				if state != "expired" {
					t.Fatalf("successful cancellation left state %q", state)
				}
				if err := provider.ExpireCheckout(context.Background(), "cs_cancel"); err != nil {
					t.Fatalf("repeated expiration must succeed: %v", err)
				}
				if expires != 2 || readbacks != tc.wantReadbacks+1 {
					t.Fatalf("repeated expiration requests: expire=%d readback=%d", expires, readbacks)
				}
			}
		})
	}
}
