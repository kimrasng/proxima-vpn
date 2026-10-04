package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
)

// SettleOutcome classifies how PaymentService.Settle disposed of a
// confirmation, purely for the caller to choose an HTTP status - none of it
// feeds back into a later Settle call.
type SettleOutcome string

const (
	// SettleGranted: the plan was granted. Happens at most once per order.
	SettleGranted SettleOutcome = "granted"
	// SettleDuplicate: this exact provider event was already processed -
	// a Stripe retry, or a captured payload replayed later. No state changed.
	SettleDuplicate SettleOutcome = "duplicate"
	// SettleIgnored: the event authenticated, but the order it names cannot
	// be settled by it (already paid by another event, cancelled, or
	// missing) - most commonly a cancelled order receiving a late payment.
	SettleIgnored SettleOutcome = "ignored"
)

// SettleResult reports what Settle did.
type SettleResult struct {
	Outcome SettleOutcome
	Reason  string
	Grant   *GrantResult // non-nil only when Outcome == SettleGranted
}

// PaymentService owns the order state machine every payment provider funnels
// into. Providers only translate their own format into a payments.Confirmation
// (see internal/payments) - none of them touch plan_orders or payment_events
// directly, which is what keeps "adding a provider" from ever needing to
// touch this file.
type PaymentService struct {
	db        *pgxpool.Pool
	plan      *PlanService
	promotion *PromotionService
	act       *ActivityService
}

// NewPaymentService creates a PaymentService.
func NewPaymentService(db *pgxpool.Pool) *PaymentService {
	return &PaymentService{
		db:        db,
		plan:      NewPlanService(db),
		promotion: NewPromotionService(db),
		act:       NewActivityService(db),
	}
}

// Settle is the single entry point every provider's confirmation reaches,
// whether it arrived as an admin's authenticated click or a hosted
// provider's signed webhook. It runs, in order:
//
//  1. Claim the event in payment_events, keyed on (provider, external_id).
//     This is the "recorded before any business logic runs" step, and it is
//     what makes a replay - a retried or a manually resent webhook - answer
//     SettleDuplicate without going anywhere near plan_orders.
//  2. Claim the order: UPDATE ... WHERE status IN ('pending','expired'). This
//     is a second, independent guard from step 1 - one order can legitimately
//     receive two different events (e.g. Stripe's sync and async success
//     events for the same session), and this is what stops the second one
//     from granting twice. Allowing 'expired' (not just 'pending') is the
//     entire "a late confirmation is still honoured" requirement: the
//     sweeper's timeout does not revoke a payment that actually arrives.
//  3. If the confirmation carries a nonzero amount, cross-check it against
//     the order's own snapshotted price_cents/currency before granting.
//  4. Grant via PlanService.GrantPlanWithDuration, using the order's own
//     duration - never the plan's default duration_days, since a plan can be
//     sold at several durations and the order is what says which one this
//     confirmation paid for.
//
// A grant failure reverts the order to 'pending' (so a retry - Stripe's own,
// or an admin retrying the click - can still succeed) and marks the event
// 'failed' so a resend past the 5-minute received-outcome window can retry
// too, rather than being blocked as a duplicate forever.
func (s *PaymentService) Settle(ctx context.Context, conf payments.Confirmation) (*SettleResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin settle: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var eventID string
	payload := conf.Payload
	if payload == nil {
		// The admin provider (and any Confirmation built by hand rather than
		// parsed from a webhook body) has no raw payload; the column is
		// NOT NULL, so an empty JSON object stands in for "nothing captured".
		payload = []byte("{}")
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO payment_events
		   (provider, external_id, order_id, amount_cents, currency, session_id,
		    payload, request_ip, user_agent, request_id, actor_type, actor_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (provider, external_id) DO UPDATE
		   SET outcome = 'received', reason = '', received_at = NOW()
		 WHERE payment_events.outcome = 'failed'
		    OR (payment_events.outcome = 'received'
		        AND payment_events.received_at < NOW() - INTERVAL '5 minutes')
		 RETURNING id`,
		conf.Provider, conf.ExternalID, nullableOrderID(conf.OrderID),
		conf.AmountCents, conf.Currency, conf.SessionID,
		payload, conf.RequestIP, conf.UserAgent, conf.RequestID,
		conf.ActorType, conf.ActorID,
	).Scan(&eventID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Insert conflicted and the WHERE didn't reopen it: a genuine
			// replay of an event already granted or still being processed.
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("commit duplicate settle: %w", err)
			}
			return &SettleResult{Outcome: SettleDuplicate, Reason: "event_already_processed"}, nil
		}
		return nil, fmt.Errorf("claim payment event: %w", err)
	}

	var userID, planID string
	var durationDays int
	var priceCents int64
	err = tx.QueryRow(ctx,
		`UPDATE plan_orders
		 SET status = 'paid', paid_at = NOW(), paid_by = $2, expired_at = NULL
		 WHERE id = $1 AND status IN ('pending','expired')
		 RETURNING user_id, plan_id, duration_days, price_cents`,
		conf.OrderID, conf.ActorLabel,
	).Scan(&userID, &planID, &durationDays, &priceCents)
	if err != nil {
		reason := "order_not_settleable"
		if errors.Is(err, pgx.ErrNoRows) {
			if err := s.recordEventOutcome(ctx, tx, eventID, "ignored", reason); err != nil {
				return nil, err
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("commit ignored settle: %w", err)
			}
			s.act.Log(ctx, Record{
				EventType:  EventPaymentIgnored,
				Severity:   SeverityWarning,
				ActorType:  conf.ActorType,
				ActorID:    conf.ActorID,
				ActorLabel: conf.ActorLabel,
				TargetType: "plan_order",
				TargetID:   conf.OrderID,
				Detail:     map[string]any{"reason": reason, "provider": conf.Provider},
			})
			return &SettleResult{Outcome: SettleIgnored, Reason: reason}, nil
		}
		return nil, fmt.Errorf("claim order: %w", err)
	}

	// Zero AmountCents means a trusted channel with nothing third-party to
	// cross-check (the admin route). A nonzero amount must match the order's
	// own snapshotted price exactly. The order claim above already flipped
	// this row to 'paid' inside the same transaction, so a mismatch has to
	// explicitly revert it here - otherwise it would commit paid regardless.
	if conf.AmountCents > 0 && conf.AmountCents != priceCents {
		if _, err := tx.Exec(ctx,
			`UPDATE plan_orders SET status = 'pending', paid_at = NULL, paid_by = '' WHERE id = $1`,
			conf.OrderID,
		); err != nil {
			return nil, fmt.Errorf("revert amount-mismatch claim: %w", err)
		}
		if err := s.recordEventOutcome(ctx, tx, eventID, "ignored", "amount_mismatch"); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit amount-mismatch settle: %w", err)
		}
		s.act.Log(ctx, Record{
			EventType:  EventPaymentIgnored,
			Severity:   SeverityError,
			ActorType:  conf.ActorType,
			ActorID:    conf.ActorID,
			ActorLabel: conf.ActorLabel,
			TargetType: "plan_order",
			TargetID:   conf.OrderID,
			Detail: map[string]any{
				"reason": "amount_mismatch", "provider": conf.Provider,
				"confirmed_cents": conf.AmountCents, "expected_cents": priceCents,
			},
		})
		return &SettleResult{Outcome: SettleIgnored, Reason: "amount_mismatch"}, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit settle claim: %w", err)
	}

	grant, err := s.plan.GrantPlanWithDuration(ctx, userID, planID, durationDays)
	if err != nil {
		s.revertFailedSettle(ctx, conf.OrderID, eventID, err)
		return nil, fmt.Errorf("grant plan: %w", err)
	}
	if err := s.promotion.Confirm(ctx, conf.OrderID); err != nil {
		fmt.Printf("payments: failed to confirm promotion for order %s: %v\n", conf.OrderID, err)
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE plan_orders
		 SET granted_plan_expires_before = $2, granted_plan_expires_after = $3
		 WHERE id = $1`,
		conf.OrderID, grant.PlanExpiresBefore, grant.PlanExpiresAt,
	); err != nil {
		// Same policy as the event mark below: the entitlement is already
		// granted, so a missing audit row is a bookkeeping gap rather than a
		// failed payment.
		fmt.Printf("payments: failed to record grant expiry evidence for order %s: %v\n", conf.OrderID, err)
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE payment_events SET outcome = 'granted', processed_at = NOW() WHERE id = $1`,
		eventID,
	); err != nil {
		// The grant already succeeded; failing to mark the event is a
		// bookkeeping gap, not a reason to report failure to the caller.
		fmt.Printf("payments: failed to mark event %s granted: %v\n", eventID, err)
	}

	s.act.Log(ctx, Record{
		EventType:  EventOrderPaid,
		Severity:   SeveritySuccess,
		ActorType:  conf.ActorType,
		ActorID:    conf.ActorID,
		ActorLabel: conf.ActorLabel,
		TargetType: "plan_order",
		TargetID:   conf.OrderID,
		Detail: map[string]any{
			"provider": conf.Provider, "duration_days": durationDays, "price_cents": priceCents,
			"grant_kind": string(grant.Kind),
		},
	})

	return &SettleResult{Outcome: SettleGranted, Grant: grant}, nil
}

// recordEventOutcome marks a payment_events row's disposition inside an
// in-flight transaction.
func (s *PaymentService) recordEventOutcome(ctx context.Context, tx pgx.Tx, eventID, outcome, reason string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE payment_events SET outcome = $2, reason = $3, processed_at = NOW() WHERE id = $1`,
		eventID, outcome, reason,
	); err != nil {
		return fmt.Errorf("record event outcome: %w", err)
	}
	return nil
}

// revertFailedSettle undoes the order claim after a grant failure, so a
// retry (Stripe's own, or an admin re-clicking) can settle the order again,
// and marks the event 'failed' so it is eligible to be reclaimed rather than
// treated as a permanent duplicate.
func (s *PaymentService) revertFailedSettle(ctx context.Context, orderID, eventID string, grantErr error) {
	if _, err := s.db.Exec(ctx,
		`UPDATE plan_orders SET status = 'pending', paid_at = NULL, paid_by = '' WHERE id = $1`,
		orderID,
	); err != nil {
		fmt.Printf("payments: failed to revert order %s after grant error: %v\n", orderID, err)
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE payment_events SET outcome = 'failed', reason = $2, processed_at = NOW() WHERE id = $1`,
		eventID, grantErr.Error(),
	); err != nil {
		fmt.Printf("payments: failed to mark event %s failed: %v\n", eventID, err)
	}
}

// nullableOrderID lets an order-less confirmation (a webhook whose metadata
// never resolved to a known order) still be recorded, since payment_events'
// job is to capture the attempt before rejecting it, not only successes.
func nullableOrderID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
