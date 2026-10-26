import { Fragment, type ReactNode } from "react";

export interface OAuthEndpoint {
  key: string;
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  path: string;
  scope?: string;
}

export const OAUTH_ENDPOINT_GROUPS: { key: "oauth" | "client"; items: OAuthEndpoint[] }[] = [
  {
    key: "oauth",
    items: [
      { key: "authorize", method: "GET", path: "/oauth/authorize" },
      { key: "token", method: "POST", path: "/api/v1/oauth/token" },
      { key: "revoke", method: "POST", path: "/api/v1/oauth/revoke" },
    ],
  },
  {
    key: "client",
    items: [
      { key: "profile", method: "GET", path: "/api/v1/client/profile", scope: "profile" },
      { key: "subscription", method: "GET", path: "/api/v1/client/subscription", scope: "subscription:read" },
      { key: "nodes", method: "GET", path: "/api/v1/client/nodes", scope: "nodes:read" },
      { key: "summary", method: "GET", path: "/api/v1/client/summary", scope: "plan:read" },
      { key: "tickets", method: "GET", path: "/api/v1/client/reset-tickets", scope: "reset_tickets:read" },
      { key: "ticketHistory", method: "GET", path: "/api/v1/client/reset-tickets/history", scope: "reset_tickets:read" },
      { key: "redeem", method: "POST", path: "/api/v1/client/reset-tickets/{id}/redeem", scope: "reset_tickets:redeem" },
      { key: "profileUpdate", method: "PUT", path: "/api/v1/client/profile", scope: "profile:write" },
      { key: "subDomains", method: "GET", path: "/api/v1/client/subscription-domains", scope: "subscription:read" },
      { key: "subRegenerate", method: "POST", path: "/api/v1/client/sub-token/regenerate", scope: "subscription:write" },
      { key: "traffic", method: "GET", path: "/api/v1/client/traffic", scope: "plan:read" },
      { key: "plans", method: "GET", path: "/api/v1/client/plans", scope: "orders:read" },
      { key: "orders", method: "GET", path: "/api/v1/client/orders", scope: "orders:read" },
      { key: "receipt", method: "GET", path: "/api/v1/client/orders/{id}/receipt", scope: "orders:read" },
      { key: "providers", method: "GET", path: "/api/v1/client/payment-providers", scope: "orders:read" },
      { key: "trial", method: "GET", path: "/api/v1/client/free-trials/subscription", scope: "orders:read" },
      { key: "promoLookup", method: "GET", path: "/api/v1/client/promotions/{code}", scope: "orders:read" },
      { key: "orderCreate", method: "POST", path: "/api/v1/client/orders", scope: "orders:write" },
      { key: "orderCancel", method: "POST", path: "/api/v1/client/orders/{id}/cancel", scope: "orders:write" },
      { key: "promoApply", method: "PATCH", path: "/api/v1/client/orders/{id}/promotion", scope: "orders:write" },
      { key: "promoRemove", method: "DELETE", path: "/api/v1/client/orders/{id}/promotion", scope: "orders:write" },
      { key: "checkout", method: "POST", path: "/api/v1/client/orders/{id}/checkout", scope: "orders:write" },
      { key: "checkoutConfirm", method: "POST", path: "/api/v1/client/orders/{id}/checkout/confirm", scope: "orders:write" },
      { key: "trialCancel", method: "POST", path: "/api/v1/client/free-trials/subscription/cancel", scope: "orders:write" },
      { key: "trialConfirm", method: "POST", path: "/api/v1/client/free-trials/subscription/confirm", scope: "orders:write" },
      { key: "billingPortal", method: "POST", path: "/api/v1/client/billing/portal", scope: "orders:write" },
      { key: "devices", method: "GET", path: "/api/v1/client/devices", scope: "devices:read" },
      { key: "deviceCreate", method: "POST", path: "/api/v1/client/devices", scope: "devices:write" },
      { key: "deviceDelete", method: "DELETE", path: "/api/v1/client/devices/{id}", scope: "devices:write" },
      { key: "faq", method: "GET", path: "/api/v1/client/support/faq", scope: "support:read" },
      { key: "supportTickets", method: "GET", path: "/api/v1/client/support/tickets", scope: "support:read" },
      { key: "supportThread", method: "GET", path: "/api/v1/client/support/tickets/{id}", scope: "support:read" },
      { key: "supportCreate", method: "POST", path: "/api/v1/client/support/tickets", scope: "support:write" },
      { key: "supportReply", method: "POST", path: "/api/v1/client/support/tickets/{id}/messages", scope: "support:write" },
      { key: "supportClose", method: "POST", path: "/api/v1/client/support/tickets/{id}/close", scope: "support:write" },
      { key: "announcements", method: "GET", path: "/api/v1/client/announcements", scope: "content:read" },
      { key: "guides", method: "GET", path: "/api/v1/client/guides", scope: "content:read" },
      { key: "guide", method: "GET", path: "/api/v1/client/guides/{id}", scope: "content:read" },
    ],
  },
];

// Lets a long URL wrap only after "/", never inside a word.
export function breakAtSlashes(url: string): ReactNode {
  return url.split(/(?<=\/)/).map((part, i) => (
    <Fragment key={i}>
      {i > 0 && <wbr />}
      <span style={{ whiteSpace: "nowrap" }}>{part}</span>
    </Fragment>
  ));
}
