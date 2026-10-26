// Response payloads shown in the OAuth docs. Shapes come from the handlers in
// api-server/internal/handlers/oauth.go, middleware/oauth_bearer.go and the
// services they call; values are sample data.

export interface ResponseCase {
  id: string;
  /** Key under admin.oauthDocs.responses.when; defaults to `id`. */
  when?: string;
  ok: boolean;
  /** Omitted for the cases where the user is sent back to the app's redirect URI. */
  status?: number;
  /** OAuth error code, shown in the label. */
  code?: string;
  headers?: string[];
  body?: string;
  /** Redirect URL shown as-is instead of an HTTP response. */
  raw?: string;
}

export interface ResponseGroup {
  key: string;
  titleKey: string;
  method: "GET" | "POST";
  path: string;
  scope?: string;
  noteKey?: string;
  cases: ResponseCase[];
}

export const STATUS_TEXT: Record<number, string> = {
  200: "OK",
  400: "Bad Request",
  401: "Unauthorized",
  403: "Forbidden",
  404: "Not Found",
  409: "Conflict",
  429: "Too Many Requests",
  500: "Internal Server Error",
};

const j = (value: unknown) => JSON.stringify(value, null, 2);
const oauthError = (error: string, error_description: string) => j({ error, error_description });

const JSON_HEADER = "Content-Type: application/json";
const NO_STORE = ["Cache-Control: no-store", "Pragma: no-cache"];
const CB = "proxima://oauth/callback";

const tokenSet = {
  access_token: "pxat_xxxxxxxxxxxxxxxx",
  token_type: "Bearer",
  expires_in: 3600,
  refresh_token: "pxrt_xxxxxxxxxxxxxxxx",
  scope: "profile subscription:read plan:read",
};

const ticket = {
  id: "6f3c1a52-9d1e-4c7b-8a53-2f0e5b7d91c4",
  name: "Launch week reset",
  granted_at: "2026-10-20T03:00:00Z",
  expires_at: "2026-11-20T03:00:00Z",
};

const bearerInvalid = 'WWW-Authenticate: Bearer realm="proxima", error="invalid_token"';

export const RESPONSE_GROUPS: ResponseGroup[] = [
  {
    key: "authorize",
    titleKey: "admin.oauthDocs.responses.ep.authorize",
    method: "GET",
    path: "/oauth/authorize",
    noteKey: "admin.oauthDocs.responses.authorizeNote",
    cases: [
      { id: "a_ok", ok: true, raw: `${CB}?code=AUTHORIZATION_CODE&state=RANDOM_STATE` },
      { id: "a_denied", ok: false, code: "access_denied", raw: `${CB}?error=access_denied&error_description=the+user+denied+the+request&state=RANDOM_STATE` },
      { id: "a_scope", ok: false, code: "invalid_scope", raw: `${CB}?error=invalid_scope&error_description=scope+nodes%3Aread+is+not+allowed+for+this+client&state=RANDOM_STATE` },
      { id: "a_pkce", ok: false, code: "invalid_request", raw: `${CB}?error=invalid_request&error_description=PKCE+with+code_challenge_method%3DS256+is+required&state=RANDOM_STATE` },
      { id: "a_type", ok: false, code: "unsupported_response_type", raw: `${CB}?error=unsupported_response_type&error_description=only+response_type%3Dcode+is+supported&state=RANDOM_STATE` },
    ],
  },
  {
    key: "token",
    titleKey: "admin.oauthDocs.responses.ep.token",
    method: "POST",
    path: "/api/v1/oauth/token",
    cases: [
      { id: "t_ok", ok: true, status: 200, headers: [JSON_HEADER, ...NO_STORE], body: j(tokenSet) },
      { id: "t_grant", ok: false, status: 400, code: "invalid_grant", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("invalid_grant", "authorization code is invalid, expired or already used") },
      { id: "t_pkce", ok: false, status: 400, code: "invalid_grant", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("invalid_grant", "PKCE verification failed") },
      { id: "t_req", ok: false, status: 400, code: "invalid_request", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("invalid_request", "code is required") },
      { id: "t_nogrant", ok: false, status: 400, code: "invalid_request", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("invalid_request", "grant_type is required") },
      { id: "t_type", ok: false, status: 400, code: "unsupported_grant_type", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("unsupported_grant_type", "grant_type password is not supported") },
      { id: "t_client", ok: false, status: 401, code: "invalid_client", headers: [JSON_HEADER, ...NO_STORE, 'WWW-Authenticate: Basic realm="oauth"'], body: oauthError("invalid_client", "client authentication failed") },
      { id: "t_slow", ok: false, status: 429, code: "slow_down", headers: [JSON_HEADER], body: oauthError("slow_down", "too many token requests") },
    ],
  },
  {
    key: "refresh",
    titleKey: "admin.oauthDocs.responses.ep.refresh",
    method: "POST",
    path: "/api/v1/oauth/token",
    cases: [
      { id: "r_ok", ok: true, status: 200, headers: [JSON_HEADER, ...NO_STORE], body: j({ ...tokenSet, access_token: "pxat_yyyyyyyyyyyyyyyy", refresh_token: "pxrt_yyyyyyyyyyyyyyyy" }) },
      { id: "r_grant", ok: false, status: 400, code: "invalid_grant", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("invalid_grant", "refresh token is invalid, expired or revoked") },
      { id: "r_scope", ok: false, status: 400, code: "invalid_scope", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("invalid_scope", "refresh cannot widen scope") },
      { id: "r_req", ok: false, status: 400, code: "invalid_request", headers: [JSON_HEADER, ...NO_STORE], body: oauthError("invalid_request", "refresh_token is required") },
    ],
  },
  {
    key: "revoke",
    titleKey: "admin.oauthDocs.responses.ep.revoke",
    method: "POST",
    path: "/api/v1/oauth/revoke",
    cases: [
      { id: "v_ok", ok: true, status: 200, headers: ["Content-Type: text/plain; charset=utf-8"], body: "OK" },
      { id: "v_client", ok: false, status: 401, code: "invalid_client", headers: [JSON_HEADER], body: oauthError("invalid_client", "client authentication failed") },
    ],
  },
  {
    key: "profile",
    titleKey: "admin.oauthApps.endpoint.profile",
    method: "GET",
    path: "/api/v1/client/profile",
    scope: "profile",
    cases: [
      { id: "p_ok", ok: true, status: 200, headers: [JSON_HEADER], body: j({ id: "3b9d6e1a-52c4-4f0e-9a77-1c8d2e4f6a90", name: "Hong Gildong", email: "user@example.com", status: "active", language: "ko" }) },
      { id: "p_500", when: "server", ok: false, status: 500, code: "server_error", headers: [JSON_HEADER], body: j({ error: "server_error" }) },
    ],
  },
  {
    key: "subscription",
    titleKey: "admin.oauthApps.endpoint.subscription",
    method: "GET",
    path: "/api/v1/client/subscription",
    scope: "subscription:read",
    cases: [
      { id: "s_ok", ok: true, status: 200, headers: [JSON_HEADER], body: j({ available: true, url: "https://sub.example.com/sub/XXXXXXXX", domains: [{ domain: "sub.example.com", is_default: true, url: "https://sub.example.com/sub/XXXXXXXX" }, { domain: "alt.example.net", is_default: false, url: "https://alt.example.net/sub/XXXXXXXX" }] }) },
      { id: "s_none", ok: true, status: 200, headers: [JSON_HEADER], body: j({ available: false, url: null, domains: [] }) },
      { id: "s_500", when: "server", ok: false, status: 500, code: "server_error", headers: [JSON_HEADER], body: j({ error: "server_error" }) },
    ],
  },
  {
    key: "nodes",
    titleKey: "admin.oauthApps.endpoint.nodes",
    method: "GET",
    path: "/api/v1/client/nodes",
    scope: "nodes:read",
    cases: [
      { id: "n_ok", ok: true, status: 200, headers: [JSON_HEADER], body: j([{ name: "Tokyo 1", country: "JP", region: "tokyo", status: "online" }, { name: "Seoul 1", country: "KR", region: "seoul", status: "offline" }]) },
      { id: "n_empty", ok: true, status: 200, headers: [JSON_HEADER], body: "[]" },
      { id: "n_500", when: "server", ok: false, status: 500, headers: [JSON_HEADER], body: j({ error: "failed to list nodes" }) },
    ],
  },
  {
    key: "summary",
    titleKey: "admin.oauthApps.endpoint.summary",
    method: "GET",
    path: "/api/v1/client/summary",
    scope: "plan:read",
    cases: [
      { id: "m_ok", ok: true, status: 200, headers: [JSON_HEADER], body: j({ plan_name: "Standard", status: "active", traffic_used: 12884901888, traffic_limit: 107374182400, plan_expires_at: "2026-11-26T03:00:00Z", devices: 2, max_devices: 5, online: 1, online_ips: 1, max_concurrent: 3, next_traffic_reset_at: "2026-11-26T03:00:00Z" }) },
      { id: "m_500", when: "server", ok: false, status: 500, headers: [JSON_HEADER], body: j({ error: "failed to fetch summary" }) },
    ],
  },
  {
    key: "tickets",
    titleKey: "admin.oauthApps.endpoint.tickets",
    method: "GET",
    path: "/api/v1/client/reset-tickets",
    scope: "reset_tickets:read",
    cases: [
      { id: "k_ok", ok: true, status: 200, headers: [JSON_HEADER], body: j([ticket]) },
      { id: "k_empty", ok: true, status: 200, headers: [JSON_HEADER], body: "[]" },
      { id: "k_500", when: "server", ok: false, status: 500, headers: [JSON_HEADER], body: j({ error: "internal server error" }) },
    ],
  },
  {
    key: "ticketHistory",
    titleKey: "admin.oauthApps.endpoint.ticketHistory",
    method: "GET",
    path: "/api/v1/client/reset-tickets/history",
    scope: "reset_tickets:read",
    cases: [
      { id: "h_ok", ok: true, status: 200, headers: [JSON_HEADER], body: j([{ ...ticket, used_at: "2026-10-24T09:12:00Z", reset_bytes: 12884901888 }]) },
      { id: "h_empty", ok: true, status: 200, headers: [JSON_HEADER], body: "[]" },
      { id: "h_500", when: "server", ok: false, status: 500, headers: [JSON_HEADER], body: j({ error: "internal server error" }) },
    ],
  },
  {
    key: "redeem",
    titleKey: "admin.oauthApps.endpoint.redeem",
    method: "POST",
    path: "/api/v1/client/reset-tickets/{id}/redeem",
    scope: "reset_tickets:redeem",
    cases: [
      { id: "d_ok", ok: true, status: 200, headers: [JSON_HEADER], body: j({ name: "Launch week reset", reset_bytes: 12884901888 }) },
      { id: "d_404", ok: false, status: 404, headers: [JSON_HEADER], body: j({ error: "reset ticket not found" }) },
      { id: "d_used", ok: false, status: 409, code: "used", headers: [JSON_HEADER], body: j({ error: "reset ticket already used", code: "used" }) },
      { id: "d_expired", ok: false, status: 409, code: "expired", headers: [JSON_HEADER], body: j({ error: "reset ticket expired", code: "expired" }) },
      { id: "d_noplan", ok: false, status: 409, code: "no_plan", headers: [JSON_HEADER], body: j({ error: "no capped plan to reset", code: "no_plan" }) },
      { id: "d_500", when: "server", ok: false, status: 500, headers: [JSON_HEADER], body: j({ error: "internal server error" }) },
    ],
  },
  {
    key: "common",
    titleKey: "admin.oauthDocs.responses.ep.common",
    method: "GET",
    path: "/api/v1/client/*",
    cases: [
      { id: "c_missing", ok: false, status: 401, code: "invalid_request", headers: [JSON_HEADER, 'WWW-Authenticate: Bearer realm="proxima"'], body: oauthError("invalid_request", "missing bearer token") },
      { id: "c_invalid", ok: false, status: 401, code: "invalid_token", headers: [JSON_HEADER, bearerInvalid], body: oauthError("invalid_token", "access token is invalid, expired or revoked") },
      { id: "c_scope", ok: false, status: 403, code: "insufficient_scope", headers: [JSON_HEADER, 'WWW-Authenticate: Bearer realm="proxima", error="insufficient_scope", scope="nodes:read"'], body: j({ error: "insufficient_scope", error_description: "this token lacks the nodes:read scope", scope: "nodes:read" }) },
      { id: "c_500", when: "server", ok: false, status: 500, code: "server_error", headers: [JSON_HEADER], body: j({ error: "server_error" }) },
    ],
  },
];
