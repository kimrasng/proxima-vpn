import { get, post, put, patch, setClientTokenType } from './client';
import type {
  UserProfile,
  UpdateProfileRequest,
  TrafficStats,
  UserPlanItem,
  Announcement,
  AvailableNode,
  UserSummary,
  CreateOrderRequest,
  CreateOrderResult,
  OrderResponse,
  OrderStatus,
  StartCheckoutResponse,
  OrderReceipt,
  PaymentProviderItem,
  PublicSubscriptionDomain,
  ResetTicket,
  UserResetTicket,
  RedeemResetTicketResult,
  SupportHelp,
  SupportTicket,
  SupportThread,
  SupportMessage,
  CreateSupportTicketRequest,
} from './types';

function applyUserClient() {
  setClientTokenType('user');
}

export function getProfile(): Promise<UserProfile> {
  applyUserClient();
  return get<UserProfile>('/api/v1/user/profile');
}

export function updateProfile(req: UpdateProfileRequest): Promise<UserProfile> {
  applyUserClient();
  return put<UserProfile>('/api/v1/user/profile', req);
}

export function getTrafficStats(): Promise<TrafficStats> {
  applyUserClient();
  return get<TrafficStats>('/api/v1/user/traffic');
}

export function getSummary(): Promise<UserSummary> {
  applyUserClient();
  return get<UserSummary>('/api/v1/user/summary');
}

// A malformed body must not reach components that map over the list: the
// reset-ticket card renders on the dashboard, and a throw there blanks it.
export async function listResetTickets(): Promise<ResetTicket[]> {
  applyUserClient();
  const response = await get<unknown>('/api/v1/user/reset-tickets');
  return Array.isArray(response) ? (response as ResetTicket[]) : [];
}

export async function listResetTicketHistory(): Promise<UserResetTicket[]> {
  applyUserClient();
  const response = await get<unknown>('/api/v1/user/reset-tickets/history');
  return Array.isArray(response) ? (response as UserResetTicket[]) : [];
}

export function redeemResetTicket(id: string): Promise<RedeemResetTicketResult> {
  applyUserClient();
  return post<RedeemResetTicketResult>(`/api/v1/user/reset-tickets/${id}/redeem`, {});
}

export function regenerateSubToken(): Promise<{ sub_token: string }> {
  applyUserClient();
  return post<{ sub_token: string }>('/api/v1/user/sub-token/regenerate');
}

export function listPlans(lang?: string): Promise<UserPlanItem[]> {
  applyUserClient();
  const query = lang ? `?lang=${encodeURIComponent(lang)}` : '';
  return get<UserPlanItem[]>(`/api/v1/user/plans${query}`);
}

export function listAvailableNodes(): Promise<AvailableNode[]> {
  applyUserClient();
  return get<AvailableNode[]>('/api/v1/user/nodes');
}

export function listAnnouncements(lang?: string): Promise<Announcement[]> {
  applyUserClient();
  const query = lang ? `?lang=${encodeURIComponent(lang.split("-")[0] ?? lang)}` : '';
  return get<Announcement[]>(`/api/v1/user/announcements${query}`);
}

// The endpoint answers with a bare order, or wraps it alongside the reason a
// supplied code was ignored. Normalising here keeps that shape out of the pages.
export async function createOrder(req: CreateOrderRequest): Promise<CreateOrderResult> {
  applyUserClient();
  const data = await post<OrderResponse | CreateOrderResult>('/api/v1/user/orders', req);
  return 'order' in data ? data : { order: data };
}

export function applyPromotionToOrder(orderId: string, code: string): Promise<OrderResponse> {
  applyUserClient();
  return patch<OrderResponse>(`/api/v1/user/orders/${orderId}/promotion`, {
    promotion_code: code,
  });
}

export interface FreeTrialRedeemResult {
  order_id: string;
  plan_name: string;
  trial_days: number;
  plan_expires_at: string;
}

export function redeemFreeTrial(code: string): Promise<FreeTrialRedeemResult> {
  applyUserClient();
  return post<FreeTrialRedeemResult>('/api/v1/user/free-trials/redeem', { code });
}

export function listMyOrders(): Promise<OrderResponse[]> {
  applyUserClient();
  return get<OrderResponse[]>('/api/v1/user/orders');
}

export function cancelOrder(id: string): Promise<void> {
  applyUserClient();
  return post<void>(`/api/v1/user/orders/${id}/cancel`);
}

export function startCheckout(orderId: string, provider: string): Promise<StartCheckoutResponse> {
  applyUserClient();
  return post<StartCheckoutResponse>(`/api/v1/user/orders/${orderId}/checkout`, { provider });
}

export function getOrderReceipt(orderId: string): Promise<OrderReceipt> {
  applyUserClient();
  return get<OrderReceipt>(`/api/v1/user/orders/${orderId}/receipt`);
}

export function confirmCheckout(orderId: string, sessionId: string): Promise<{ status: OrderStatus }> {
  applyUserClient();
  return post<{ status: OrderStatus }>(`/api/v1/user/orders/${orderId}/checkout/confirm`, { session_id: sessionId });
}

export function listPaymentProviders(): Promise<PaymentProviderItem[]> {
  applyUserClient();
  return get<PaymentProviderItem[]>('/api/v1/user/payment-providers');
}

function isSubscriptionHostname(value: unknown): value is string {
  if (
    typeof value !== 'string' || value.length > 253 ||
    !/^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(value)
  ) return false;
  let parsed: URL;
  try {
    parsed = new URL(`https://${value}/`);
  } catch (error) {
    if (error instanceof TypeError) return false;
    throw error;
  }
  // URL canonicalizes shorthand and hexadecimal IPv4 before classification.
  return !/^\d+\.\d+\.\d+\.\d+$/.test(parsed.hostname);
}

export async function listPublicSubscriptionDomains(): Promise<PublicSubscriptionDomain[]> {
  applyUserClient();
  const response = await get<unknown>('/api/v1/user/subscription-domains');
  if (!Array.isArray(response)) return [];
  return response.flatMap((row: unknown): PublicSubscriptionDomain[] => {
    if (
      typeof row !== 'object' || row === null ||
      !('id' in row) || typeof row.id !== 'string' || row.id.trim() === '' ||
      !('domain' in row) || !isSubscriptionHostname(row.domain) ||
      !('display_order' in row) || typeof row.display_order !== 'number' ||
      !Number.isInteger(row.display_order) || row.display_order < 0 ||
      !('is_default' in row) || typeof row.is_default !== 'boolean'
    ) return [];
    return [{ id: row.id, domain: row.domain, display_order: row.display_order, is_default: row.is_default }];
  });
}

export function getSupportHelp(query = ''): Promise<SupportHelp> {
  applyUserClient();
  const suffix = query.trim() ? `?q=${encodeURIComponent(query.trim())}` : '';
  return get<SupportHelp>(`/api/v1/user/support/faq${suffix}`);
}

export function listSupportTickets(): Promise<SupportTicket[]> {
  applyUserClient();
  return get<SupportTicket[]>('/api/v1/user/support/tickets');
}

export function createSupportTicket(req: CreateSupportTicketRequest): Promise<{ id: string }> {
  applyUserClient();
  return post<{ id: string }>('/api/v1/user/support/tickets', req);
}

export function getSupportTicket(id: string): Promise<SupportThread> {
  applyUserClient();
  return get<SupportThread>(`/api/v1/user/support/tickets/${id}`);
}

export function postSupportMessage(id: string, body: string): Promise<SupportMessage> {
  applyUserClient();
  return post<SupportMessage>(`/api/v1/user/support/tickets/${id}/messages`, { body });
}

export function closeSupportTicket(id: string): Promise<void> {
  applyUserClient();
  return post<void>(`/api/v1/user/support/tickets/${id}/close`, {});
}
