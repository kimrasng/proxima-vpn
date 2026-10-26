import { get, post, setClientTokenType } from "./client";

export interface PaymentMethodSummary {
  readonly brand: string;
  readonly last4: string;
  readonly exp_month: number;
  readonly exp_year: number;
  readonly funding: string;
  readonly wallet?: string;
}

export interface TrialSubscription {
  readonly id: string;
  readonly plan_name: string;
  readonly code: string;
  readonly status: string;
  readonly price_cents: number;
  readonly currency: string;
  readonly duration_days: number;
  readonly trial_ends_at: string | null;
  readonly current_period_end: string | null;
  readonly cancel_at_period_end: boolean;
  /** Card details retrieved from Stripe; null when no card was returned. */
  readonly payment_method: PaymentMethodSummary | null;
  readonly payment_method_unavailable?: boolean;
}

export function currentTrial() {
  setClientTokenType("user");
  return get<TrialSubscription | null>("/api/v1/user/free-trials/subscription");
}

export function cancelTrial() {
  setClientTokenType("user");
  return post<TrialSubscription>("/api/v1/user/free-trials/subscription/cancel", {});
}

export function openBillingPortal() {
  setClientTokenType("user");
  return post<{ readonly url: string }>("/api/v1/user/billing/portal", {});
}

export function confirmTrial(id: string, sessionId: string) {
  setClientTokenType("user");
  return post<TrialSubscription>("/api/v1/user/free-trials/subscription/confirm", {
    id, session_id: sessionId,
  });
}
