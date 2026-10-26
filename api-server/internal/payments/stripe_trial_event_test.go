package payments

import (
	"encoding/json"
	"testing"

	"github.com/stripe/stripe-go/v86"
)

func trialTestEvent(t *testing.T, kind, body string) stripe.Event {
	t.Helper()
	var event stripe.Event
	if err := json.Unmarshal([]byte(`{"id":"evt_trial","type":"`+kind+`","created":1900000000,"data":{"object":`+body+`}}`), &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestTrialEventIgnoresOrdinaryCheckout(t *testing.T) {
	// Given
	event := trialTestEvent(t, "checkout.session.completed",
		`{"id":"cs_once","mode":"payment","status":"complete","metadata":{"order_id":"ordinary"}}`)
	// When
	result, err := parseTrialEvent(event)
	// Then
	if err != nil || result != nil {
		t.Fatalf("ordinary checkout routed to trial settlement: %+v %v", result, err)
	}
}

func TestTrialEventPreservesCompletedSessionIdentity(t *testing.T) {
	// Given
	event := trialTestEvent(t, "checkout.session.completed",
		`{"id":"cs_trial","mode":"subscription","status":"complete",
		"metadata":{"trial_subscription_id":"local"},"subscription":"sub_trial",
		"customer":"cus_trial","customer_details":{"email":"trial@example.test"}}`)
	// When
	result, err := parseTrialEvent(event)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.LocalID != "local" || result.SessionID != "cs_trial" ||
		result.SubscriptionID != "sub_trial" || result.CustomerID != "cus_trial" ||
		result.Email != "trial@example.test" || result.ID != "evt_trial" {
		t.Fatalf("trial identity lost: %+v", result)
	}
}

func TestTrialEventRejectsIncompleteCheckout(t *testing.T) {
	event := trialTestEvent(t, "checkout.session.completed",
		`{"id":"cs_trial","mode":"subscription","status":"open","metadata":{"trial_subscription_id":"local"}}`)
	if _, err := parseTrialEvent(event); err == nil {
		t.Fatal("incomplete session accepted")
	}
}

func TestTrialEventPreservesInvoiceAmountAndSubscription(t *testing.T) {
	// Given
	event := trialTestEvent(t, "invoice.paid",
		`{"id":"in_renewal","amount_paid":1500,"currency":"usd","status":"paid","billing_reason":"subscription_cycle",
		"lines":{"data":[{"quantity":1,"period":{"start":1900000000,"end":1902592000},
		"parent":{"subscription_item_details":{"subscription_item":"si_trial","proration":false}}}]},
		"parent":{"type":"subscription_details","subscription_details":{
			"subscription":"sub_trial","metadata":{"trial_subscription_id":"local"}
		}}}`)
	// When
	result, err := parseTrialEvent(event)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.InvoiceID != "in_renewal" || result.SubscriptionID != "sub_trial" ||
		result.LocalID != "local" || result.AmountCents != 1500 || result.Currency != "usd" ||
		result.Status != "paid" || result.PeriodEnd != 1902592000 {
		t.Fatalf("invoice snapshot lost: %+v", result)
	}
}

func TestTrialEventPreservesCancellationAndPeriod(t *testing.T) {
	event := trialTestEvent(t, "customer.subscription.updated",
		`{"id":"sub_trial","status":"trialing","trial_end":1900604800,"cancel_at_period_end":true,
		"metadata":{"trial_subscription_id":"local"},"items":{"data":[{"id":"si_trial","current_period_end":1900604800}]}}`)
	result, err := parseTrialEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.CancelAtEnd || result.Status != "trialing" ||
		result.TrialEnd != 1900604800 || result.PeriodEnd != 1900604800 {
		t.Fatalf("cancellation state lost: %+v", result)
	}
}
