const BASELINE_DAYS = 30;

export interface PlanSavings {
  percent: number;
  amountCents: number;
}

export function formatPriceCents(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`;
}

// Compares a duration's per-day rate against the 30-day row's, so a longer term
// priced at the same daily rate reports no saving rather than a large one.
// 30d @ $10 vs 90d @ $27: monthlyEquivalent 3000, saved 300, percent 10.
export function planSavings(
  prices: { duration_days: number; price_cents: number }[],
  durationDays: number,
): PlanSavings | null {
  if (durationDays === BASELINE_DAYS) return null;

  const baseline = prices.find((p) => p.duration_days === BASELINE_DAYS);
  if (!baseline || baseline.price_cents <= 0) return null;

  const row = prices.find((p) => p.duration_days === durationDays);
  if (!row || durationDays <= 0) return null;

  const monthlyEquivalent = baseline.price_cents * (durationDays / BASELINE_DAYS);
  const amountCents = Math.round(monthlyEquivalent - row.price_cents);
  const percent = Math.round(
    (1 - row.price_cents / durationDays / (baseline.price_cents / BASELINE_DAYS)) * 100,
  );

  if (amountCents <= 0 || percent <= 0) return null;
  return { percent, amountCents };
}
