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
}
