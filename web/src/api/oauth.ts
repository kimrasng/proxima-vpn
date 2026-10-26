import { get, post, put, del, setClientTokenType } from './client';

export const OAUTH_SCOPES = [
  'profile',
  'profile:write',
  'subscription:read',
  'subscription:write',
  'nodes:read',
  'plan:read',
  'reset_tickets:read',
  'reset_tickets:redeem',
  'orders:read',
  'orders:write',
  'support:read',
  'support:write',
  'devices:read',
  'devices:write',
  'content:read',
] as const;

// Scopes that let an app change the account, shown with a warning on consent.
export const WRITE_SCOPES: readonly string[] = [
  'profile:write',
  'subscription:write',
  'reset_tickets:redeem',
  'orders:write',
  'support:write',
  'devices:write',
];
export type OAuthScope = (typeof OAUTH_SCOPES)[number];

// i18n keys cannot contain ":" (it is the namespace separator), so scopes are
// looked up by this key form: "subscription:read" -> "subscription_read".
export function scopeKey(scope: string): string {
  return scope.replace(/[:]/g, '_');
}

export type OAuthClientType = 'confidential' | 'public';

export interface OAuthApp {
  id: string;
  client_id: string;
  name: string;
  description: string;
  client_type: OAuthClientType;
  redirect_uris: string[];
  allowed_scopes: OAuthScope[];
  is_active: boolean;
  has_secret: boolean;
  secret_rotated_at: string | null;
  active_users: number;
  created_at: string;
  updated_at: string;
  client_secret?: string;
}

export interface OAuthAppInput {
  name: string;
  description: string;
  client_type: OAuthClientType;
  redirect_uris: string[];
  allowed_scopes: OAuthScope[];
  is_active: boolean;
}

export interface OAuthAuthorizeInfo {
  client: { client_id: string; name: string; description: string };
  redirect_uri: string;
  scopes: OAuthScope[];
}

export interface OAuthAuthorizeError {
  error: string;
  error_description?: string;
  redirect_to?: string;
}

export interface OAuthGrant {
  client_id: string;
  name: string;
  description: string;
  scopes: OAuthScope[];
  granted_at: string;
  last_used_at: string | null;
}

export function listOAuthApps(): Promise<OAuthApp[]> {
  setClientTokenType('admin');
  return get<OAuthApp[]>('/api/v1/admin/oauth-apps');
}

export function createOAuthApp(input: OAuthAppInput): Promise<OAuthApp> {
  setClientTokenType('admin');
  return post<OAuthApp>('/api/v1/admin/oauth-apps', input);
}

export function updateOAuthApp(id: string, input: OAuthAppInput): Promise<OAuthApp> {
  setClientTokenType('admin');
  return put<OAuthApp>(`/api/v1/admin/oauth-apps/${encodeURIComponent(id)}`, input);
}

export function rotateOAuthAppSecret(id: string): Promise<{ client_secret: string }> {
  setClientTokenType('admin');
  return post<{ client_secret: string }>(`/api/v1/admin/oauth-apps/${encodeURIComponent(id)}/secret`);
}

export interface OAuthAppUser {
  user_id: string;
  email: string;
  name: string;
  scopes: OAuthScope[];
  granted_at: string;
  last_used_at: string | null;
  active_tokens: number;
}

export function listOAuthAppUsers(id: string): Promise<OAuthAppUser[]> {
  setClientTokenType('admin');
  return get<OAuthAppUser[]>(`/api/v1/admin/oauth-apps/${encodeURIComponent(id)}/users`);
}

export function revokeOAuthAppUser(id: string, userId: string): Promise<{ revoked: number }> {
  setClientTokenType('admin');
  return del<{ revoked: number }>(`/api/v1/admin/oauth-apps/${encodeURIComponent(id)}/users/${encodeURIComponent(userId)}`);
}

export function revokeAllOAuthAppUsers(id: string): Promise<{ revoked: number }> {
  setClientTokenType('admin');
  return del<{ revoked: number }>(`/api/v1/admin/oauth-apps/${encodeURIComponent(id)}/users`);
}

export function deleteOAuthApp(id: string): Promise<void> {
  setClientTokenType('admin');
  return del<void>(`/api/v1/admin/oauth-apps/${encodeURIComponent(id)}`);
}

export function getOAuthAuthorize(search: string): Promise<OAuthAuthorizeInfo> {
  setClientTokenType('user');
  return get<OAuthAuthorizeInfo>(`/api/v1/user/oauth/authorize${search}`);
}

export function decideOAuthAuthorize(params: Record<string, string>, approve: boolean): Promise<{ redirect_to: string }> {
  setClientTokenType('user');
  return post<{ redirect_to: string }>('/api/v1/user/oauth/authorize', { ...params, approve });
}

export function listOAuthGrants(): Promise<OAuthGrant[]> {
  setClientTokenType('user');
  return get<OAuthGrant[]>('/api/v1/user/oauth/grants');
}

export function revokeOAuthGrant(clientId: string): Promise<void> {
  setClientTokenType('user');
  return del<void>(`/api/v1/user/oauth/grants/${encodeURIComponent(clientId)}`);
}
