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

// Promotion windows are entered and shown in KST regardless of the admin's
// browser time zone. KST has no daylight saving, so a fixed +9h offset is exact:
// a Date shifted by it reads KST wall-clock values through the UTC getters.
const KST_OFFSET_MS = 9 * 60 * 60 * 1000;

function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

function toKstWallClock(date: Date): Date {
  return new Date(date.getTime() + KST_OFFSET_MS);
}

// Expects a Date already shifted by toKstWallClock.
function toDateString(kst: Date): string {
  return `${kst.getUTCFullYear()}-${pad2(kst.getUTCMonth() + 1)}-${pad2(kst.getUTCDate())}`;
}

function splitIsoDate(iso: string): { date: string; time: string } {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return { date: "", time: "00:00" };
  const kst = toKstWallClock(parsed);
  return { date: toDateString(kst), time: `${pad2(kst.getUTCHours())}:${pad2(kst.getUTCMinutes())}` };
}

// "2026.10.26 14:30" in KST, for read-only displays such as the promotions table.
export function formatKst(iso: string): string {
  const { date, time } = splitIsoDate(iso);
  return date ? `${date.replace(/-/g, ".")} ${time}` : "";
}

export type WindowUnit = "days" | "months";

// A window of N days or months that starts now and includes today, so "30 days"
// ends on day 30 at 23:59 and "1 month" from Oct 26 ends on Nov 25 23:59. Month
// arithmetic clamps to the last day (Jan 31 + 1 month -> Feb 28).
export function windowFromToday(
  amount: number,
  unit: WindowUnit,
  now: Date = new Date(),
): Pick<PromotionForm, "validFromDate" | "validFromTime" | "validUntilDate" | "validUntilTime"> {
  const kstNow = toKstWallClock(now);
  const end = new Date(Date.UTC(kstNow.getUTCFullYear(), kstNow.getUTCMonth(), kstNow.getUTCDate()));
  if (unit === "months") {
    const day = end.getUTCDate();
    end.setUTCDate(1);
    end.setUTCMonth(end.getUTCMonth() + amount);
    const lastDay = new Date(Date.UTC(end.getUTCFullYear(), end.getUTCMonth() + 1, 0)).getUTCDate();
    end.setUTCDate(Math.min(day, lastDay));
  } else {
    end.setUTCDate(end.getUTCDate() + amount);
  }
  end.setUTCDate(end.getUTCDate() - 1);
  return {
    validFromDate: toDateString(kstNow),
    validFromTime: `${pad2(kstNow.getUTCHours())}:${pad2(kstNow.getUTCMinutes())}`,
    validUntilDate: toDateString(end),
    validUntilTime: "23:59",
  };
}

// Whole calendar days covered by the window, counting both the start and end day.
export function windowSpanDays(fromDate: string, untilDate: string): number | null {
  const from = new Date(`${fromDate}T00:00:00Z`);
  const until = new Date(`${untilDate}T00:00:00Z`);
  if (Number.isNaN(from.getTime()) || Number.isNaN(until.getTime())) return null;
  return Math.round((until.getTime() - from.getTime()) / 86_400_000) + 1;
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

// Form values are KST wall-clock time; the API stores an absolute instant.
export function kstDateTimeToIso(date: string, time: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !/^\d{2}:\d{2}(:\d{2})?$/.test(time)) return null;
  const parsed = new Date(`${date}T${time.length === 5 ? `${time}:00` : time}+09:00`);
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

  const validFrom = kstDateTimeToIso(form.validFromDate, form.validFromTime);
  const validUntil = kstDateTimeToIso(form.validUntilDate, form.validUntilTime);
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
    valid_from: kstDateTimeToIso(form.validFromDate, form.validFromTime) ?? "",
    valid_until: kstDateTimeToIso(form.validUntilDate, form.validUntilTime) ?? "",
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

export function generatePromoCode(length = 8): string {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const bytes = crypto.getRandomValues(new Uint8Array(length));
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}
