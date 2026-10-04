import { get, post, del, put, patch, setClientTokenType } from './client';
import type {
  UserProfile,
  UpdateProfileRequest,
  TrafficStats,
  Device,
  CreateDeviceRequest,
  UserPlanItem,
  Announcement,
  AvailableNode,
  UserSummary,
  CreateOrderRequest,
  CreateOrderResult,
  OrderResponse,
  StartCheckoutResponse,
  PaymentProviderItem,
  PublicSubscriptionDomain,
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

export function regenerateSubToken(): Promise<{ sub_token: string }> {
  applyUserClient();
  return post<{ sub_token: string }>('/api/v1/user/sub-token/regenerate');
}

export function listDevices(): Promise<Device[]> {
  applyUserClient();
  return get<Device[]>('/api/v1/user/devices');
}

export function createDevice(req: CreateDeviceRequest): Promise<Device> {
  applyUserClient();
  return post<Device>('/api/v1/user/devices', req);
}

export function deleteDevice(id: string): Promise<void> {
  applyUserClient();
  return del<void>(`/api/v1/user/devices/${id}`);
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

export function listAnnouncements(): Promise<Announcement[]> {
  applyUserClient();
  return get<Announcement[]>('/api/v1/user/announcements');
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
