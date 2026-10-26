package database

var promotionKindSchema = []string{
	`CREATE TABLE IF NOT EXISTS free_trial_promotions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		code TEXT NOT NULL UNIQUE,
		plan_id UUID REFERENCES plans(id),
		duration_days INT NOT NULL CHECK (duration_days > 0),
		trial_days INT NOT NULL CHECK (trial_days > 0),
		max_redemptions INT,
		max_redemptions_per_user INT NOT NULL DEFAULT 1,
		requires_card BOOLEAN NOT NULL DEFAULT true,
		is_active BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS affiliate_partners (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		name TEXT NOT NULL,
		code TEXT NOT NULL UNIQUE,
		commission_rate NUMERIC(5,2) NOT NULL CHECK (commission_rate >= 0 AND commission_rate <= 100),
		discount_type TEXT NOT NULL CHECK (discount_type IN ('percent','fixed')),
		discount_value BIGINT NOT NULL CHECK (discount_value > 0),
		is_active BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS affiliate_commissions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		partner_id UUID REFERENCES affiliate_partners(id),
		order_id UUID REFERENCES plan_orders(id),
		order_amount BIGINT NOT NULL,
		commission_amount BIGINT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid')),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_affiliate_commissions_partner ON affiliate_commissions(partner_id, created_at)`,
	// One commission per paid order, so a replayed settlement cannot pay twice.
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_affiliate_commissions_order ON affiliate_commissions(order_id) WHERE order_id IS NOT NULL`,
	// Which partner referred the order; set while pending, read when it is paid.
	`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS affiliate_partner_id UUID REFERENCES affiliate_partners(id)`,
	`ALTER TABLE plan_orders ADD COLUMN IF NOT EXISTS free_trial_id UUID REFERENCES free_trial_promotions(id)`,
	`CREATE TABLE IF NOT EXISTS free_trial_redemptions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		promotion_id UUID NOT NULL REFERENCES free_trial_promotions(id),
		user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		order_id UUID NOT NULL UNIQUE REFERENCES plan_orders(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_free_trial_redemptions_promo_user ON free_trial_redemptions(promotion_id, user_id)`,
	`CREATE TABLE IF NOT EXISTS stripe_trial_subscriptions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		promotion_id UUID NOT NULL REFERENCES free_trial_promotions(id),
		plan_id UUID NOT NULL REFERENCES plans(id),
		customer_id TEXT,
		checkout_session_id TEXT UNIQUE,
		subscription_id TEXT UNIQUE,
		price_cents BIGINT NOT NULL CHECK (price_cents > 0),
		currency TEXT NOT NULL,
		duration_days INT NOT NULL CHECK (duration_days BETWEEN 1 AND 365),
		trial_days INT NOT NULL CHECK (trial_days BETWEEN 1 AND 730),
		status TEXT NOT NULL DEFAULT 'checkout' CHECK
			(status IN ('checkout','trialing','active','past_due','unpaid','paused','canceled','expired')),
		cancel_at_period_end BOOLEAN NOT NULL DEFAULT false,
		last_event_created BIGINT NOT NULL DEFAULT 0,
		checkout_expires_at TIMESTAMPTZ NOT NULL,
		trial_ends_at TIMESTAMPTZ,
		current_period_end TIMESTAMPTZ,
		trial_order_id UUID UNIQUE REFERENCES plan_orders(id),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_stripe_trial_user_open
		ON stripe_trial_subscriptions(user_id)
		WHERE status NOT IN ('canceled','expired')`,
	`ALTER TABLE stripe_trial_subscriptions ADD COLUMN IF NOT EXISTS last_event_created BIGINT NOT NULL DEFAULT 0`,
	`CREATE TABLE IF NOT EXISTS stripe_trial_invoices (
		invoice_id TEXT PRIMARY KEY,
		subscription_id UUID NOT NULL REFERENCES stripe_trial_subscriptions(id),
		order_id UUID NOT NULL UNIQUE REFERENCES plan_orders(id),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS stripe_trial_events (
		event_id TEXT PRIMARY KEY,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`ALTER TABLE stripe_trial_invoices ADD COLUMN IF NOT EXISTS period_start BIGINT`,
	`ALTER TABLE stripe_trial_invoices ADD COLUMN IF NOT EXISTS period_end BIGINT`,
}
