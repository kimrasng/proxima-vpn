import type { TFunction } from "i18next";
import type { PlanPrice, PlanFeature } from "../../api/types";
import { hasFieldErrors } from "../../utils/formValidation";

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

// Translated messages keyed by the field they belong to; an absent key means
// the field is valid. Row arrays line up index-for-index with the form rows.
export interface PlanFormErrors {
  fields: Partial<Record<
    "name" | "node_group_id" | "id" | "traffic_limit" | "duration_days" | "speed_limit" | "max_devices" | "max_concurrent",
    string
  >>;
  prices: { durationDays?: string; priceDollars?: string }[];
  features: (string | undefined)[];
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const isPositiveInteger = (value: string) => Number.isSafeInteger(Number(value)) && Number(value) > 0;

export function validatePlanForm(form: PlanForm, t: TFunction): PlanFormErrors {
  const fields: PlanFormErrors["fields"] = {};
  if (!form.name.trim()) fields.name = t("admin.plans.editor.nameRequired");
  if (!form.node_group_id) fields.node_group_id = t("admin.plans.editor.nodeGroupRequired");
  if (form.id.trim() && !uuidPattern.test(form.id.trim())) fields.id = t("admin.plans.editor.idInvalid");
  if (!isPositiveInteger(form.duration_days)) fields.duration_days = t("admin.plans.pricing.durationInvalid");
  if (!isPositiveInteger(form.max_devices)) fields.max_devices = t("admin.plans.editor.positiveInteger");
  // Blank optional limits mean "unlimited" / "same as max devices".
  if (form.max_concurrent && !isPositiveInteger(form.max_concurrent)) {
    fields.max_concurrent = t("admin.plans.editor.positiveInteger");
  }
  if (form.traffic_limit && !(Number.isFinite(Number(form.traffic_limit)) && Number(form.traffic_limit) >= 0)) {
    fields.traffic_limit = t("admin.plans.editor.nonNegative");
  }
  if (form.speed_limit && !(Number.isSafeInteger(Number(form.speed_limit)) && Number(form.speed_limit) >= 0)) {
    fields.speed_limit = t("admin.plans.editor.nonNegativeInteger");
  }

  const seen = new Set<number>();
  const prices = form.prices.map((row) => {
    const rowErrors: PlanFormErrors["prices"][number] = {};
    const days = Number(row.durationDays);
    if (!Number.isInteger(days) || days <= 0) {
      rowErrors.durationDays = t("admin.plans.pricing.durationInvalid");
    } else if (seen.has(days)) {
      rowErrors.durationDays = t("admin.plans.pricing.durationDuplicate", { days });
    } else {
      seen.add(days);
    }
    const dollars = Number(row.priceDollars);
    if (!row.priceDollars.trim()) {
      rowErrors.priceDollars = t("admin.plans.editor.priceRequired");
    } else if (!Number.isFinite(dollars) || dollars < 0) {
      rowErrors.priceDollars = t("admin.plans.pricing.priceInvalid");
    }
    return rowErrors;
  });

  const features = form.features.map((row) =>
    featureLanguages.some((code) => row.text[code]?.trim()) ? undefined : t("admin.plans.features.textRequired"),
  );

  return { fields, prices, features };
}

export function hasPlanFormErrors(errors: PlanFormErrors): boolean {
  return (
    hasFieldErrors(errors.fields) ||
    errors.prices.some((row) => hasFieldErrors(row)) ||
    errors.features.some(Boolean)
  );
}

// Both converters assume validatePlanForm already passed.
export function toPlanPrices(rows: PriceRow[]): PlanPrice[] {
  return rows.map((row) => ({
    duration_days: Number(row.durationDays),
    price_cents: Math.round(Number(row.priceDollars) * 100),
  }));
}

export function toPlanFeatures(rows: FeatureRow[]): PlanFeature[] {
  return rows.map((row) => {
    const text: Record<string, string> = {};
    for (const code of featureLanguages) {
      const value = row.text[code]?.trim();
      if (value) text[code] = value;
    }
    return { included: row.included, text };
  });
}
