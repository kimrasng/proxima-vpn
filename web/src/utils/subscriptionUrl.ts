// Each account has exactly one subscription URL. The server picks the config
// format from the client's User-Agent; the path suffix is only a fallback for
// apps it cannot recognise.
export type SubscriptionClientType = "clash-meta" | "sing-box" | "v2ray" | "clash" | "wireguard";

export const CLIENT_TYPES: ReadonlyArray<{ id: SubscriptionClientType; label: string; appsKey: string }> = [
  { id: "clash-meta", label: "Clash Meta (mihomo)", appsKey: "user.devices.clientApps.clashMeta" },
  { id: "sing-box", label: "sing-box", appsKey: "user.devices.clientApps.singBox" },
  { id: "v2ray", label: "V2Ray / Xray", appsKey: "user.devices.clientApps.v2ray" },
  { id: "clash", label: "Clash", appsKey: "user.devices.clientApps.clash" },
  { id: "wireguard", label: "WireGuard", appsKey: "user.devices.clientApps.wireguard" },
];

export function getAccountSubscriptionUrl(subToken: string, domain?: string, clientType?: SubscriptionClientType): string {
  const path = `/sub/${encodeURIComponent(subToken)}${clientType ? `/${clientType}` : ""}`;
  const url = new URL(path, window.location.origin);
  if (domain) {
    url.protocol = "https:";
    url.port = "";
    url.host = domain;
  }
  return url.toString();
}
