import type { Device } from "../api/types";

export const SUBSCRIPTION_FORMATS = [
  { id: "v2ray", label: "V2Ray" },
  { id: "clash", label: "Clash" },
  { id: "singbox", label: "Sing-box" },
  { id: "surfboard", label: "Surfboard" },
  { id: "quantumult", label: "Quantumult" },
  { id: "wireguard", label: "WireGuard" },
];

export function getAccountSubscriptionUrl(subToken: string, format?: string, domain?: string): string {
  return formatSubscriptionUrl(`/sub/${encodeURIComponent(subToken)}`, format, domain);
}

export function getSubscriptionUrl(device: Device, format?: string, domain?: string): string {
  return formatSubscriptionUrl(device.subscription_url || `/sub/${device.xray_uuid}`, format, domain);
}

function formatSubscriptionUrl(raw: string, format?: string, domain?: string): string {
  // Retain the encoded route, token and unrelated query parameters, including
  // when a device was issued an absolute URL on an older subscription host.
  const url = new URL(raw, window.location.origin);
  if (domain) {
    url.protocol = "https:";
    url.port = "";
    url.host = domain;
  }
  if (format && format !== "v2ray") url.searchParams.set("format", format);
  else if (format === "v2ray" || domain) url.searchParams.delete("format");
  return url.toString();
}
