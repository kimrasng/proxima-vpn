package payments

import (
	"encoding/json"
	"fmt"

	"github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
)

type TrialEvent struct {
	ID             string
	Kind           string
	LocalID        string
	SessionID      string
	CheckoutURL    string
	SubscriptionID string
	CustomerID     string
	InvoiceID      string
	Status         string
	Currency       string
	Email          string
	AmountCents    int64
	TrialEnd       int64
	PeriodEnd      int64
	PeriodStart    int64
	BillingReason  string
	ItemID         string
	Quantity       int64
	Proration      bool
	CancelAtEnd    bool
	Created        int64
}

type TrialEventVerifier interface {
	VerifyTrialEvent([]byte, func(string) string) (*TrialEvent, error)
}

func (p *StripeProvider) VerifyTrialEvent(body []byte, header func(string) string) (*TrialEvent, error) {
	event, err := webhook.ConstructEvent(body, header("Stripe-Signature"), p.webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("verify trial event signature: %w", err)
	}
	return parseTrialEvent(event)
}

func parseTrialEvent(event stripe.Event) (*TrialEvent, error) {
	result := &TrialEvent{ID: event.ID, Kind: string(event.Type), Created: event.Created}
	switch event.Type {
	case "checkout.session.completed", "checkout.session.expired":
		var session stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
			return nil, fmt.Errorf("decode trial checkout: %w", err)
		}
		return trialEventFromSession(&session, event.ID, string(event.Type))
	case "customer.subscription.updated", "customer.subscription.deleted":
		var subscription stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
			return nil, fmt.Errorf("decode trial subscription: %w", err)
		}
		result.LocalID = subscription.Metadata["trial_subscription_id"]
		result.SubscriptionID, result.Status = subscription.ID, string(subscription.Status)
		result.TrialEnd, result.CancelAtEnd = subscription.TrialEnd, subscription.CancelAtPeriodEnd
		if subscription.Items != nil {
			for _, item := range subscription.Items.Data {
				if item.CurrentPeriodEnd > result.PeriodEnd {
					result.PeriodEnd = item.CurrentPeriodEnd
				}
			}
		}
	case "invoice.paid", "invoice.payment_failed":
		var invoice stripe.Invoice
		if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
			return nil, fmt.Errorf("decode trial invoice: %w", err)
		}
		if invoice.Parent == nil || invoice.Parent.SubscriptionDetails == nil ||
			invoice.Parent.SubscriptionDetails.Subscription == nil {
			return nil, nil
		}
		result.SubscriptionID = invoice.Parent.SubscriptionDetails.Subscription.ID
		result.LocalID = invoice.Parent.SubscriptionDetails.Metadata["trial_subscription_id"]
		result.InvoiceID, result.AmountCents = invoice.ID, invoice.AmountPaid
		result.Currency, result.Status = string(invoice.Currency), string(invoice.Status)
		result.BillingReason = string(invoice.BillingReason)
		if invoice.Lines != nil && !invoice.Lines.HasMore && len(invoice.Lines.Data) == 1 {
			line := invoice.Lines.Data[0]
			result.Quantity = line.Quantity
			if line.Period != nil {
				result.PeriodStart, result.PeriodEnd = line.Period.Start, line.Period.End
			}
			if line.Parent != nil && line.Parent.SubscriptionItemDetails != nil {
				result.ItemID = line.Parent.SubscriptionItemDetails.SubscriptionItem
				result.Proration = line.Parent.SubscriptionItemDetails.Proration
			}
		}
	default:
		return nil, nil
	}
	return result, nil
}

func trialEventFromSession(session *stripe.CheckoutSession, eventID, kind string) (*TrialEvent, error) {
	localID := session.Metadata["trial_subscription_id"]
	if localID == "" || session.Mode != stripe.CheckoutSessionModeSubscription {
		return nil, nil
	}
	if kind == "checkout.session.completed" && session.Status != stripe.CheckoutSessionStatusComplete {
		return nil, fmt.Errorf("trial checkout is not complete")
	}
	result := &TrialEvent{ID: eventID, Kind: kind, LocalID: localID,
		SessionID: session.ID, CheckoutURL: session.URL, Email: session.CustomerEmail}
	if session.CustomerDetails != nil && result.Email == "" {
		result.Email = session.CustomerDetails.Email
	}
	if session.Subscription != nil {
		result.SubscriptionID = session.Subscription.ID
	}
	if session.Customer != nil {
		result.CustomerID = session.Customer.ID
	}
	return result, nil
}
