import { Fragment, type ReactNode } from "react";

export interface OAuthEndpoint {
  key: string;
  method: "GET" | "POST";
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
