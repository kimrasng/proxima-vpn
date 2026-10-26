import { get, post, put, del, setClientTokenType } from "./client";

export interface FreeTrialInput {
  code: string;
  plan_id: string | null;
  duration_days: number;
  trial_days: number;
  max_redemptions: number | null;
  max_redemptions_per_user: number;
  requires_card: boolean;
  is_active: boolean;
}
export interface FreeTrialPromotion extends FreeTrialInput {
  id: string;
  created_at: string;
}
export interface AffiliateInput {
  name: string;
  code: string;
  commission_rate: number;
  discount_type: "percent" | "fixed";
  discount_value: number;
  is_active: boolean;
}
export interface AffiliatePartner extends AffiliateInput {
  id: string;
  created_at: string;
}
export interface AffiliateCommission {
  id: string;
  partner_id: string | null;
  order_id: string | null;
  order_amount: number;
  commission_amount: number;
  status: "pending" | "paid";
  created_at: string;
}
export interface AffiliateCommissionSummary {
  commissions: AffiliateCommission[];
  total_orders: number;
  total_revenue: number;
  commission_owed: number;
  commission_paid: number;
}
const trialPath = "/api/v1/admin/free-trials";
const affiliatePath = "/api/v1/admin/affiliates";
export function listFreeTrials() {
  setClientTokenType("admin");
  return get<FreeTrialPromotion[]>(trialPath);
}
export function saveFreeTrial(input: FreeTrialInput, id?: string) {
  setClientTokenType("admin");
  return id ? put<FreeTrialPromotion>(`${trialPath}/${id}`, input) : post<FreeTrialPromotion>(trialPath, input);
}
export function deleteFreeTrial(id: string) {
  setClientTokenType("admin");
  return del<void>(`${trialPath}/${id}`);
}
export function listAffiliates() {
  setClientTokenType("admin");
  return get<AffiliatePartner[]>(affiliatePath);
}
export function saveAffiliate(input: AffiliateInput, id?: string) {
  setClientTokenType("admin");
  return id ? put<AffiliatePartner>(`${affiliatePath}/${id}`, input) : post<AffiliatePartner>(affiliatePath, input);
}
export function deleteAffiliate(id: string) {
  setClientTokenType("admin");
  return del<void>(`${affiliatePath}/${id}`);
}
export function getAffiliateCommissions(id: string) {
  setClientTokenType("admin");
  return get<AffiliateCommissionSummary>(`${affiliatePath}/${id}/commissions`);
}
export function markAffiliateCommissionPaid(partnerId: string, commissionId: string) {
  setClientTokenType("admin");
  return put<{ status: "paid" }>(`${affiliatePath}/${partnerId}/commissions/${commissionId}/paid`);
}

export interface PromoLandingInfo {
  type: "coupon" | "trial" | "affiliate";
  code: string;
  discount_description: string;
  plan_name?: string;
  trial_days?: number;
  is_valid: boolean;
}
export function getPromoLanding(code: string) {
  return get<PromoLandingInfo>(`/api/v1/promo/${encodeURIComponent(code)}`);
}
