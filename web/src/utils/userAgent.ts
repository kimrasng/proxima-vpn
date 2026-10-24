export type ClientInfo = {
  browser: string;
  os: string;
  device: "desktop" | "mobile" | "tablet" | "app" | "unknown";
};

const BROWSERS: [RegExp, string][] = [
  [/Edg(?:e|A|iOS)?\/(\d+)/, "Edge"],
  [/OPR\/(\d+)/, "Opera"],
  [/SamsungBrowser\/(\d+)/, "Samsung Internet"],
  [/Whale\/(\d+)/, "Whale"],
  [/CriOS\/(\d+)/, "Chrome"],
  [/FxiOS\/(\d+)/, "Firefox"],
  [/Firefox\/(\d+)/, "Firefox"],
  [/Chrome\/(\d+)/, "Chrome"],
  [/Version\/(\d+)[.\d]* .*Safari\//, "Safari"],
];

const APPS: [RegExp, string][] = [
  [/clash[-.]?verge/i, "Clash Verge"],
  [/clash/i, "Clash"],
  [/sing-box|sfa|sfi/i, "sing-box"],
  [/shadowrocket/i, "Shadowrocket"],
  [/v2rayn/i, "v2rayN"],
  [/v2rayng/i, "v2rayNG"],
  [/stash/i, "Stash"],
  [/python-urllib|curl|wget|go-http-client|okhttp/i, ""],
];

function osOf(ua: string): string {
  let m = /iPhone OS (\d+)[_.](\d+)/.exec(ua) ?? /CPU OS (\d+)[_.](\d+)/.exec(ua);
  if (m) return `iOS ${m[1]}.${m[2]}`;
  m = /Android (\d+(?:\.\d+)?)/.exec(ua);
  if (m) return `Android ${m[1]}`;
  m = /Windows NT (\d+\.\d+)/.exec(ua);
  if (m) return m[1] === "10.0" ? "Windows 10/11" : m[1] === "6.3" ? "Windows 8.1" : m[1] === "6.1" ? "Windows 7" : "Windows";
  m = /Mac OS X (\d+)[_.](\d+)/.exec(ua);
  if (m) return `macOS ${m[1]}.${m[2]}`;
  if (/CrOS/.test(ua)) return "ChromeOS";
  if (/Linux/.test(ua)) return "Linux";
  return "";
}

export function describeClient(ua: string): ClientInfo {
  const trimmed = ua.trim();
  if (!trimmed) return { browser: "", os: "", device: "unknown" };
  const os = osOf(trimmed);
  const device: ClientInfo["device"] = /iPad|Tablet/.test(trimmed) ? "tablet"
    : /Mobile|iPhone|Android/.test(trimmed) ? "mobile"
    : os ? "desktop" : "unknown";
  for (const [pattern, name] of BROWSERS) {
    const m = pattern.exec(trimmed);
    if (m) return { browser: `${name} ${m[1]}`, os, device };
  }
  for (const [pattern, name] of APPS) {
    if (pattern.test(trimmed)) return { browser: name || trimmed.split(/[\s/]/)[0] || "", os, device: "app" };
  }
  return { browser: trimmed.split(/[\s/]/)[0] ?? "", os, device };
}
