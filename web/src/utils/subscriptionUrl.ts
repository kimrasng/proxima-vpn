// The portal publishes one account URL. The server selects the config format
// using the requesting app's User-Agent; client-specific paths remain a server
// compatibility feature, not a choice exposed to users here.
export function getAccountSubscriptionUrl(subToken: string, domain?: string): string {
  const url = new URL(`/sub/${encodeURIComponent(subToken)}`, window.location.origin);
  if (domain) {
    url.protocol = "https:";
    url.port = "";
    url.host = domain;
  }
  return url.toString();
}
