import type { TFunction } from "i18next";
import type { PromotionCode, PromotionDiscountType, PromotionRequest } from "../../api/types";

// Every field is edited as a string and converted only on submit, matching the
// plan form. `discountValue` carries percent points or dollars depending on
// `discountType`, so the conversion to cents is deferred to the same place.
export interface PromotionForm {
  code: string;
  discountType: PromotionDiscountType;
  discountValue: string;
  validFromDate: string;
  validFromTime: string;
  validUntilDate: string;
  validUntilTime: string;
  minOrderDollars: string;
  unlimitedRedemptions: boolean;
  maxRedemptions: string;
  maxRedemptionsPerUser: string;
  firstPurchaseOnly: boolean;
  planIds: string[];
  durationDays: string;
  allowedUserIds: string;
  isActive: boolean;
}

export const emptyPromotionForm: PromotionForm = {
  code: "",
  discountType: "percent",
  discountValue: "",
  validFromDate: "",
  validFromTime: "00:00",
  validUntilDate: "",
  validUntilTime: "23:59",
  minOrderDollars: "0",
  unlimitedRedemptions: true,
  maxRedemptions: "",
  maxRedemptionsPerUser: "1",
  firstPurchaseOnly: false,
  planIds: [],
  durationDays: "",
  allowedUserIds: "",
  isActive: true,
};

function splitIsoDate(iso: string): { date: string; time: string } {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return { date: "", time: "00:00" };
  const pad = (n: number) => String(n).padStart(2, "0");
  return {
    date: `${parsed.getFullYear()}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())}`,
    time: `${pad(parsed.getHours())}:${pad(parsed.getMinutes())}`,
  };
}

export function toPromotionForm(promotion: PromotionCode): PromotionForm {
  const from = splitIsoDate(promotion.valid_from);
  const until = splitIsoDate(promotion.valid_until);
  return {
    code: promotion.code,
    discountType: promotion.discount_type,
    discountValue:
      promotion.discount_type === "percent"
        ? String(promotion.discount_value)
        : (promotion.discount_value / 100).toFixed(2),
    validFromDate: from.date,
    validFromTime: from.time,
    validUntilDate: until.date,
    validUntilTime: until.time,
    minOrderDollars: (promotion.min_order_cents / 100).toFixed(2),
    unlimitedRedemptions: promotion.max_redemptions === null,
    maxRedemptions: promotion.max_redemptions === null ? "" : String(promotion.max_redemptions),
    maxRedemptionsPerUser: String(promotion.max_redemptions_per_user),
    firstPurchaseOnly: promotion.first_purchase_only,
    planIds: [...promotion.plan_ids],
    durationDays: promotion.duration_days.join(", "),
    allowedUserIds: promotion.allowed_user_ids.join(", "),
    isActive: promotion.is_active,
  };
}

// The list fields are comma-separated free text, so an entry the admin is still
// typing ("30, ") must not become an empty element.
export function splitList(value: string): string[] {
  return value
    .split(",")
    .map((entry) => entry.trim())
    .filter((entry) => entry.length > 0);
}

export function localDateTimeToIso(date: string, time: string): string | null {
  const parsed = new Date(`${date}T${time.length === 5 ? `${time}:00` : time}`);
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString();
}

// Translated messages keyed by the field they belong to; an absent key means
// the field is valid.
export type PromotionFormErrors = Partial<Record<
  "code" | "discountValue" | "minOrderDollars" | "validFrom" | "validUntil" | "maxRedemptions" | "maxRedemptionsPerUser" | "durationDays",
  string
>>;

export function validatePromotionForm(form: PromotionForm, t: TFunction): PromotionFormErrors {
  const errors: PromotionFormErrors = {};
  if (!form.code.trim()) errors.code = t("admin.promotions.form.codeRequired");

  const rawValue = Number(form.discountValue);
  if (!form.discountValue.trim()) {
    errors.discountValue = t("admin.promotions.form.discountValueRequired");
  } else if (!Number.isFinite(rawValue)) {
    errors.discountValue = t("admin.promotions.form.discountValueInvalid");
  } else if (form.discountType === "percent") {
    if (!Number.isInteger(rawValue) || rawValue < 1 || rawValue > 100) {
      errors.discountValue = t("admin.promotions.form.percentRangeInvalid");
    }
  } else if (rawValue <= 0) {
    errors.discountValue = t("admin.promotions.form.fixedRangeInvalid");
  }

  const minOrderDollars = Number(form.minOrderDollars || "0");
  if (!Number.isFinite(minOrderDollars) || minOrderDollars < 0) {
    errors.minOrderDollars = t("admin.promotions.form.minOrderInvalid");
  }

  const validFrom = localDateTimeToIso(form.validFromDate, form.validFromTime);
  const validUntil = localDateTimeToIso(form.validUntilDate, form.validUntilTime);
  if (!validFrom) errors.validFrom = t("admin.promotions.form.validFromRequired");
  if (!validUntil) {
    errors.validUntil = t("admin.promotions.form.validUntilRequired");
  } else if (validFrom && new Date(validUntil) <= new Date(validFrom)) {
    errors.validUntil = t("admin.promotions.form.windowOrderInvalid");
  }

  if (!form.unlimitedRedemptions) {
    const parsed = Number(form.maxRedemptions);
    if (!form.maxRedemptions.trim() || !Number.isInteger(parsed) || parsed <= 0) {
      errors.maxRedemptions = t("admin.promotions.form.maxRedemptionsInvalid");
    }
  }

  const perUser = Number(form.maxRedemptionsPerUser);
  if (!form.maxRedemptionsPerUser.trim() || !Number.isInteger(perUser) || perUser <= 0) {
    errors.maxRedemptionsPerUser = t("admin.promotions.form.perUserInvalid");
  }

  if (splitList(form.durationDays).some((entry) => !Number.isInteger(Number(entry)) || Number(entry) <= 0)) {
    errors.durationDays = t("admin.promotions.form.durationDaysInvalid");
  }
  return errors;
}

// Assumes validatePromotionForm already passed.
export function toPromotionRequest(form: PromotionForm, includeActive: boolean): PromotionRequest {
  const rawValue = Number(form.discountValue);
  return {
    code: form.code.trim(),
    discount_type: form.discountType,
    discount_value: form.discountType === "percent" ? rawValue : Math.round(rawValue * 100),
    valid_from: localDateTimeToIso(form.validFromDate, form.validFromTime) ?? "",
    valid_until: localDateTimeToIso(form.validUntilDate, form.validUntilTime) ?? "",
    min_order_cents: Math.round(Number(form.minOrderDollars || "0") * 100),
    max_redemptions: form.unlimitedRedemptions ? null : Number(form.maxRedemptions),
    max_redemptions_per_user: Number(form.maxRedemptionsPerUser),
    first_purchase_only: form.firstPurchaseOnly,
    plan_ids: form.planIds,
    duration_days: splitList(form.durationDays).map(Number),
    allowed_user_ids: splitList(form.allowedUserIds),
    ...(includeActive ? { is_active: form.isActive } : {}),
  };
}
