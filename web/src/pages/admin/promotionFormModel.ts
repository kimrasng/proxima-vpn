import type { PromotionCode, PromotionDiscountType } from "../../api/types";

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
