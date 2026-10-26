package payments

import (
	"testing"
	"time"
)

func TestTrialCheckoutCollectsCardAndSnapshotsRecurringTerms(t *testing.T) {
	// Given
	provider := &StripeProvider{panelURL: "https://panel.example", currency: "usd"}
	req := TrialCheckoutRequest{
		ID: "local-trial", UserEmail: "trial@example.test", Currency: "usd",
		PriceCents: 1500, DurationDays: 30, TrialDays: 7,
		ExpiresAt: time.Unix(1900000000, 0),
	}
	// When
	params, err := provider.trialParams(req)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if *params.Mode != "subscription" || *params.PaymentMethodCollection != "always" ||
		len(params.PaymentMethodTypes) != 1 || *params.PaymentMethodTypes[0] != "card" {
		t.Fatal("trial must require card collection in subscription mode")
	}
	price := params.LineItems[0].PriceData
	if *price.UnitAmount != 1500 || *price.Currency != "usd" ||
		*price.Recurring.Interval != "day" || *price.Recurring.IntervalCount != 30 {
		t.Fatal("recurring price differs from persisted billing terms")
	}
	if *params.SubscriptionData.TrialPeriodDays != 7 ||
		params.Metadata["trial_subscription_id"] != req.ID ||
		params.SubscriptionData.Metadata["trial_subscription_id"] != req.ID {
		t.Fatal("trial duration or subscription identity was lost")
	}
}

func TestTrialCheckoutRejectsUnsupportedBillingTerms(t *testing.T) {
	for _, req := range []TrialCheckoutRequest{
		{PriceCents: 0, DurationDays: 30, TrialDays: 7},
		{PriceCents: 100, DurationDays: 0, TrialDays: 7},
		{PriceCents: 100, DurationDays: 366, TrialDays: 7},
		{PriceCents: 100, DurationDays: 30, TrialDays: 0},
		{PriceCents: 100, DurationDays: 30, TrialDays: 731},
	} {
		t.Run(time.Duration(req.DurationDays).String()+"/"+time.Duration(req.TrialDays).String(), func(t *testing.T) {
			provider := &StripeProvider{}
			if _, err := provider.trialParams(req); err == nil {
				t.Fatal("unsupported terms accepted")
			}
		})
	}
}
