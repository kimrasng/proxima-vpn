import type { PlanPrice, PlanFeature } from "../../api/types";

export const featureLanguages = ["ko", "en", "zh"] as const;

// Edited in dollars, stored as integer cents; conversion happens only on submit.
export interface PriceRow {
  durationDays: string;
  priceDollars: string;
}

export interface FeatureRow {
  included: boolean;
  text: Record<string, string>;
}

export interface PlanForm {
  id: string;
  advertise: boolean;
  name: string;
  traffic_limit: string;
  duration_days: string;
  max_devices: string;
  max_concurrent: string;
  speed_limit: string;
  node_group_id: string;
  is_active: boolean;
  prices: PriceRow[];
  features: FeatureRow[];
}

export const emptyForm: PlanForm = {
  id: "",
  advertise: false,
  name: "",
  traffic_limit: "",
  duration_days: "30",
  max_devices: "3",
  max_concurrent: "",
  speed_limit: "",
  node_group_id: "",
  is_active: true,
  prices: [],
  features: [],
};

export function toPriceRows(prices: PlanPrice[] | undefined): PriceRow[] {
  return (prices ?? []).map((p) => ({
    durationDays: String(p.duration_days),
    priceDollars: (p.price_cents / 100).toFixed(2),
  }));
}

export function toFeatureRows(features: PlanFeature[] | undefined): FeatureRow[] {
  return (features ?? []).map((f) => ({ included: f.included, text: { ...f.text } }));
}
