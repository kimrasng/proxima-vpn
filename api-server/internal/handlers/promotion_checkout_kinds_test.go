package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/api-server/internal/database"
	"github.com/proximavpn/proxima-vpn/api-server/internal/payments"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
)

func checkoutKindsDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testOrderDB(t)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func seedPendingOrder(t *testing.T, pool *pgxpool.Pool, userID, planID string) string {
	t.Helper()
	var orderID string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO plan_orders (user_id, plan_id, duration_days, price_cents, expires_at)
		 VALUES ($1, $2, 30, 1000, NOW() + INTERVAL '1 hour') RETURNING id::text`, userID, planID,
	).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return orderID
}

func seedPartner(t *testing.T, pool *pgxpool.Pool, active bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	partnerID, code := uuid.NewString(), "AFF-"+uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO affiliate_partners(id,name,code,commission_rate,discount_type,discount_value,is_active)
		VALUES($1,'Partner',$2,12.5,'percent',20,$3)`, partnerID, code, active); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM affiliate_commissions WHERE partner_id=$1`, partnerID)
		_, _ = pool.Exec(ctx, `UPDATE plan_orders SET affiliate_partner_id=NULL WHERE affiliate_partner_id=$1`, partnerID)
		_, _ = pool.Exec(ctx, `DELETE FROM affiliate_partners WHERE id=$1`, partnerID)
	})
	return partnerID, code
}

func seedTrial(t *testing.T, pool *pgxpool.Pool, planID string, maxRedemptions *int, perUser int, requiresCard bool) string {
	t.Helper()
	ctx := context.Background()
	trialID, code := uuid.NewString(), "TRIAL-"+uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO free_trial_promotions(id,code,plan_id,duration_days,trial_days,max_redemptions,max_redemptions_per_user,requires_card)
		VALUES($1,$2,$3,30,7,$4,$5,$6)`, trialID, code, planID, maxRedemptions, perUser, requiresCard); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM free_trial_redemptions WHERE promotion_id=$1`, trialID)
		_, _ = pool.Exec(ctx, `UPDATE plan_orders SET free_trial_id=NULL WHERE free_trial_id=$1`, trialID)
		_, _ = pool.Exec(ctx, `DELETE FROM free_trial_promotions WHERE id=$1`, trialID)
	})
	return code
}

func rejectReason(err error) services.PromotionRejectReason {
	var reject *services.PromotionRejectError
	if errors.As(err, &reject) {
		return reject.Reason
	}
	return ""
}

func TestAffiliateCode_DiscountsOrderAndBooksOneCommission(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	partnerID, code := seedPartner(t, pool, true)
	orderID := seedPendingOrder(t, pool, userID, planID)
	h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}

	// Given: a $10.00 pending order. When: the partner's 20% code is applied.
	resp := orderResponse{ID: orderID}
	if err := h.applyPromotion(ctx, &resp, userID, code); err != nil {
		t.Fatalf("apply affiliate: %v", err)
	}
	// Then: the customer pays $8.00 and the order is attributed to the partner.
	if resp.PriceCents != 800 || resp.DiscountCents != 200 || resp.PromotionCode != code {
		t.Fatalf("unexpected order after affiliate: %+v", resp)
	}

	// When: the same payment confirmation arrives twice and a second event too.
	pay := services.NewPaymentService(pool)
	conf := payments.Confirmation{Provider: "admin", ExternalID: "evt-" + orderID, OrderID: orderID, ActorType: "admin", ActorLabel: "test"}
	if res, err := pay.Settle(ctx, conf); err != nil || res.Outcome != services.SettleGranted {
		t.Fatalf("settle: %+v %v", res, err)
	}
	if _, err := pay.Settle(ctx, conf); err != nil {
		t.Fatalf("replay: %v", err)
	}
	conf.ExternalID = "evt2-" + orderID
	if _, err := pay.Settle(ctx, conf); err != nil {
		t.Fatalf("second event: %v", err)
	}
	// Then: exactly one commission of 12.5% of $8.00 = 100 cents exists.
	var count int
	var amount, commission int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MAX(order_amount),0), COALESCE(MAX(commission_amount),0)
		FROM affiliate_commissions WHERE partner_id=$1`, partnerID).Scan(&count, &amount, &commission); err != nil {
		t.Fatal(err)
	}
	if count != 1 || amount != 800 || commission != 100 {
		t.Fatalf("commissions count=%d amount=%d commission=%d", count, amount, commission)
	}
}

func TestAffiliateCode_CancelledOrderEarnsNothingAndInactiveIsRejected(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	partnerID, code := seedPartner(t, pool, true)
	_, inactiveCode := seedPartner(t, pool, false)
	orderID := seedPendingOrder(t, pool, userID, planID)
	h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}

	// Given: an inactive partner code. When: applied. Then: rejected as inactive.
	if reason := rejectReason(h.applyPromotion(ctx, &orderResponse{ID: orderID}, userID, inactiveCode)); reason != services.RejectInactive {
		t.Fatalf("inactive partner reason = %q", reason)
	}
	// Given: an attributed order. When: it is cancelled before payment.
	if err := h.applyPromotion(ctx, &orderResponse{ID: orderID}, userID, code); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE plan_orders SET status='cancelled', cancelled_at=NOW() WHERE id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	res, err := services.NewPaymentService(pool).Settle(ctx, payments.Confirmation{Provider: "admin", ExternalID: "late-" + orderID, OrderID: orderID, ActorType: "admin"})
	if err != nil || res.Outcome != services.SettleIgnored {
		t.Fatalf("late settle on cancelled order: %+v %v", res, err)
	}
	// Then: no commission is booked.
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM affiliate_commissions WHERE partner_id=$1`, partnerID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("commission count=%d err=%v", count, err)
	}
}

func TestFreeTrialCode_AttachesToNormalOrder(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := seedTrial(t, pool, planID, nil, 1, false)
	orderID := seedPendingOrder(t, pool, userID, planID)
	h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}

	// Given a pending normal order, applying a trial keeps that order pending at zero.
	resp := orderResponse{ID: orderID}
	if err := h.applyPromotion(ctx, &resp, userID, code); err != nil {
		t.Fatal(err)
	}
	if resp.ID != orderID || resp.PriceCents != 0 || resp.DiscountCents != 1000 || resp.PromotionCode != code || resp.Status != "pending" {
		t.Fatalf("trial order = %+v", resp)
	}
	result, err := services.NewFreeTrialService(pool).RedeemOrder(ctx, userID, code, orderID)
	if err != nil || result.OrderID != orderID || result.TrialDays != 7 {
		t.Fatalf("trial checkout = %+v, %v", result, err)
	}
}

func TestFreeTrialRedeem_GrantsPlanAsZeroOrderAndEnforcesRules(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := seedTrial(t, pool, planID, nil, 1, false)
	cardCode := seedTrial(t, pool, planID, nil, 1, true)
	trials := services.NewFreeTrialService(pool)

	// Given: a 7-day trial. When: redeemed. Then: a paid zero-amount order grants the plan.
	res, err := trials.Redeem(ctx, userID, code)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	var status, provider string
	var price int64
	var days int
	if err := pool.QueryRow(ctx, `SELECT status, provider, price_cents, duration_days FROM plan_orders WHERE id=$1`, res.OrderID).
		Scan(&status, &provider, &price, &days); err != nil {
		t.Fatal(err)
	}
	if status != "paid" || provider != services.ProviderFreeTrial || price != 0 || days != 7 || res.PlanExpiresAt.IsZero() {
		t.Fatalf("order status=%s provider=%s price=%d days=%d result=%+v", status, provider, price, days, res)
	}
	// When: the trial ends and the same user redeems again. Then: the per-user cap rejects it.
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_expires_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if reason := rejectReason(func() error { _, err := trials.Redeem(ctx, userID, code); return err }()); reason != services.RejectUserCapReached {
		t.Fatalf("second redeem reason = %q", reason)
	}
	// When: a card-required trial is redeemed. Then: it is refused, not granted free.
	if reason := rejectReason(func() error { _, err := trials.Redeem(ctx, userID, cardCode); return err }()); reason != services.RejectCardRequiredUnsupported {
		t.Fatalf("card trial reason = %q", reason)
	}
	// When: the plan is deactivated. Then: a fresh trial for it is rejected.
	other := seedTrial(t, pool, planID, nil, 1, false)
	if _, err := pool.Exec(ctx, `UPDATE plans SET is_active=false WHERE id=$1`, planID); err != nil {
		t.Fatal(err)
	}
	if reason := rejectReason(func() error { _, err := trials.Redeem(ctx, userID, other); return err }()); reason != services.RejectPlanNotEligible {
		t.Fatalf("inactive plan reason = %q", reason)
	}
}

func TestFreeTrialRedeem_OverallCapHoldsUnderConcurrency(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	cap := 2
	var planID string
	users := make([]string, 6)
	for i := range users {
		u, p, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
		users[i] = u
		if i == 0 {
			planID = p
		}
	}
	code := seedTrial(t, pool, planID, &cap, 1, false)
	trials := services.NewFreeTrialService(pool)

	// Given: a trial capped at 2. When: 6 users redeem at the same moment.
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, len(users))
	for i, u := range users {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			<-start
			_, results[i] = trials.Redeem(ctx, u, code)
		}(i, u)
	}
	close(start)
	wg.Wait()
	// Then: exactly 2 succeed and the rest hit the overall cap.
	ok, capped := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case rejectReason(err) == services.RejectOverallCapReached:
			capped++
		default:
			t.Fatalf("unexpected redeem error: %v", err)
		}
	}
	if ok != cap || capped != len(users)-cap {
		t.Fatalf("ok=%d capped=%d", ok, capped)
	}
}

func TestFreeTrialRedeem_RejectsUserWithRunningPlan(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	code := seedTrial(t, pool, planID, nil, 1, false)
	// Given: a user who already paid for a plan that runs for another year.
	if _, err := pool.Exec(ctx, `UPDATE users SET plan_id=$2, plan_expires_at=NOW()+INTERVAL '365 days' WHERE id=$1`, userID, planID); err != nil {
		t.Fatal(err)
	}
	// When: they redeem a trial. Then: it is refused and no order or slot is used.
	_, err := services.NewFreeTrialService(pool).Redeem(ctx, userID, code)
	if reason := rejectReason(err); reason != services.RejectActivePlan {
		t.Fatalf("reason = %q (err %v)", reason, err)
	}
	var orders, slots int
	if err := pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM plan_orders WHERE user_id=$1), (SELECT COUNT(*) FROM free_trial_redemptions WHERE user_id=$1)`, userID).Scan(&orders, &slots); err != nil {
		t.Fatal(err)
	}
	if orders != 0 || slots != 0 {
		t.Fatalf("orders=%d slots=%d", orders, slots)
	}
}

func TestPromotionSwap_CouponAndAffiliateReplaceEachOtherCleanly(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	_, affCode := seedPartner(t, pool, true)
	couponCode := "CPN-" + uuid.NewString()[:8]
	var couponID string
	if err := pool.QueryRow(ctx, `INSERT INTO promotion_codes(code,discount_type,discount_value,valid_from,valid_until,max_redemptions)
		VALUES($1,'fixed',300,NOW()-INTERVAL '1 day',NOW()+INTERVAL '1 day',1) RETURNING id::text`, couponCode).Scan(&couponID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_redemptions WHERE promotion_id=$1`, couponID)
		_, _ = pool.Exec(ctx, `UPDATE plan_orders SET promotion_id=NULL WHERE promotion_id=$1`, couponID)
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_codes WHERE id=$1`, couponID)
	})
	orderID := seedPendingOrder(t, pool, userID, planID)
	h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}
	state := func() (promo, partner *string, price int64, redeemed int) {
		if err := pool.QueryRow(ctx, `SELECT o.promotion_id::text, o.affiliate_partner_id::text, o.price_cents, p.redeemed_count
			FROM plan_orders o, promotion_codes p WHERE o.id=$1 AND p.id=$2`, orderID, couponID).Scan(&promo, &partner, &price, &redeemed); err != nil {
			t.Fatal(err)
		}
		return
	}

	// Given: the coupon on the order. When: switched to the affiliate code.
	if err := h.applyPromotion(ctx, &orderResponse{ID: orderID}, userID, couponCode); err != nil {
		t.Fatal(err)
	}
	if err := h.applyPromotion(ctx, &orderResponse{ID: orderID}, userID, affCode); err != nil {
		t.Fatal(err)
	}
	// Then: the coupon slot is back and only the partner is attributed.
	promo, partner, price, redeemed := state()
	if promo != nil || partner == nil || price != 800 || redeemed != 0 {
		t.Fatalf("after coupon->affiliate promo=%v partner=%v price=%d redeemed=%d", promo, partner, price, redeemed)
	}
	// When: switched back to the coupon. Then: the partner is cleared and the slot is reserved again.
	if err := h.applyPromotion(ctx, &orderResponse{ID: orderID}, userID, couponCode); err != nil {
		t.Fatal(err)
	}
	promo, partner, price, redeemed = state()
	if promo == nil || partner != nil || price != 700 || redeemed != 1 {
		t.Fatalf("after affiliate->coupon promo=%v partner=%v price=%d redeemed=%d", promo, partner, price, redeemed)
	}
}

func TestPromotionPrecedence_CouponWinsOverSameNamedAffiliate(t *testing.T) {
	pool := checkoutKindsDB(t)
	ctx := context.Background()
	userID, planID, _ := seedFreeOrderData(t, pool, ctx, uuid.NewString()[:8])
	partnerID, code := seedPartner(t, pool, true)
	var couponID string
	if err := pool.QueryRow(ctx, `INSERT INTO promotion_codes(code,discount_type,discount_value,valid_from,valid_until)
		VALUES($1,'fixed',100,NOW()-INTERVAL '1 day',NOW()+INTERVAL '1 day') RETURNING id::text`, code).Scan(&couponID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_redemptions WHERE promotion_id=$1`, couponID)
		_, _ = pool.Exec(ctx, `UPDATE plan_orders SET promotion_id=NULL WHERE promotion_id=$1`, couponID)
		_, _ = pool.Exec(ctx, `DELETE FROM promotion_codes WHERE id=$1`, couponID)
	})
	orderID := seedPendingOrder(t, pool, userID, planID)
	h := &UserOrderHandler{db: pool, activity: services.NewActivityService(pool), promotion: services.NewPromotionService(pool)}
	// Given: one string that is both a coupon and a partner code. When: applied.
	resp := orderResponse{ID: orderID}
	if err := h.applyPromotion(ctx, &resp, userID, code); err != nil {
		t.Fatal(err)
	}
	// Then: the coupon applies, as the landing page advertises, and no partner is attributed.
	var partner *string
	if err := pool.QueryRow(ctx, `SELECT affiliate_partner_id::text FROM plan_orders WHERE id=$1`, orderID).Scan(&partner); err != nil {
		t.Fatal(err)
	}
	if resp.PriceCents != 900 || partner != nil {
		t.Fatalf("price=%d partner=%v (partner %s)", resp.PriceCents, partner, partnerID)
	}
}
