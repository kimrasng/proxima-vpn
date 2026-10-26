import { get, post, setClientTokenType } from "./client";

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
}

type TrialStart =
  | { readonly mode: "redirect"; readonly redirect_url: string }
  | { readonly order_id: string; readonly plan_expires_at: string };

export function currentTrial() {
  setClientTokenType("user");
  return get<TrialSubscription | null>("/api/v1/user/free-trials/subscription");
}

export function startTrial(code: string) {
  setClientTokenType("user");
  return post<TrialStart>("/api/v1/user/free-trials/redeem", { code });
}

export function cancelTrial() {
  setClientTokenType("user");
  return post<TrialSubscription>("/api/v1/user/free-trials/subscription/cancel", {});
}

export function confirmTrial(id: string, sessionId: string) {
  setClientTokenType("user");
  return post<TrialSubscription>("/api/v1/user/free-trials/subscription/confirm", {
    id, session_id: sessionId,
  });
}
