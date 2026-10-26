import clashverge from "../../assets/apps/clashverge.png";
import flclash from "../../assets/apps/flclash.png";
import hiddify from "../../assets/apps/hiddify.png";
import quantumultx from "../../assets/apps/quantumultx.png";
import shadowrocket from "../../assets/apps/shadowrocket.png";
import singbox from "../../assets/apps/singbox.png";
import streisand from "../../assets/apps/streisand.png";
import v2rayn from "../../assets/apps/v2rayn.png";
import v2rayng from "../../assets/apps/v2rayng.png";

export type Platform = "windows" | "macos" | "ios" | "android" | "linux";
export const PLATFORMS: Platform[] = [
  "windows",
  "macos",
  "ios",
  "android",
  "linux",
];

export interface ConnectApp {
  id: string;
  name: string;
  /** Bundled icon; the in-house app uses a monogram instead. */
  icon?: string;
  /** Builds the one-tap import link, or null for apps that only accept a pasted URL. */
  deeplink: ((url: string, name: string) => string) | null;
  /** Store or release page per platform the app supports. */
  install: Partial<Record<Platform, string | null>>;
  /** Shown first and highlighted. */
  recommended?: boolean;
}

const enc = encodeURIComponent;

// Shadowrocket expects standard base64 (not URL-safe) for sub:// links.
function base64(text: string): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(text)));
}

/**
 * Our own client. Scheme and store links are placeholders until the app team
 * confirms them; a null install link renders as "coming soon".
 */
export const PROXIMA_APP: ConnectApp = {
  id: "proxima",
  name: "Proxima",
  deeplink: (url, name) => `proxima://import?url=${enc(url)}&name=${enc(name)}`,
  install: { windows: null, macos: null, ios: null, android: null },
  recommended: true,
};

const GITHUB = {
  clashVerge:
    "https://github.com/clash-verge-rev/clash-verge-rev/releases/latest",
  flclash: "https://github.com/chen08209/FlClash/releases/latest",
  hiddify: "https://github.com/hiddify/hiddify-app/releases/latest",
  v2rayn: "https://github.com/2dust/v2rayN/releases/latest",
  v2rayng: "https://github.com/2dust/v2rayNG/releases/latest",
  singbox: "https://github.com/SagerNet/sing-box/releases/latest",
};

export const THIRD_PARTY_APPS: ConnectApp[] = [
  {
    id: "clash-verge",
    name: "Clash Verge Rev",
    icon: clashverge,
    deeplink: (url, name) =>
      `clash://install-config?url=${enc(url)}&name=${enc(name)}`,
    install: {
      windows: GITHUB.clashVerge,
      macos: GITHUB.clashVerge,
      linux: GITHUB.clashVerge,
    },
  },
  {
    id: "flclash",
    name: "FlClash",
    icon: flclash,
    deeplink: (url, name) =>
      `clash://install-config?url=${enc(url)}&name=${enc(name)}`,
    install: {
      windows: GITHUB.flclash,
      macos: GITHUB.flclash,
      android: GITHUB.flclash,
      linux: GITHUB.flclash,
    },
  },
  {
    id: "shadowrocket",
    name: "Shadowrocket",
    icon: shadowrocket,
    deeplink: (url, name) => `sub://${base64(url)}#${enc(name)}`,
    install: { ios: "https://apps.apple.com/app/id932747118" },
  },
  {
    id: "streisand",
    name: "Streisand",
    icon: streisand,
    deeplink: (url, name) => `streisand://import/${url}#${enc(name)}`,
    install: { ios: "https://apps.apple.com/app/id6450534064" },
  },
  {
    id: "hiddify",
    name: "Hiddify",
    icon: hiddify,
    deeplink: (url, name) => `hiddify://import/${url}#${enc(name)}`,
    install: {
      windows: GITHUB.hiddify,
      macos: GITHUB.hiddify,
      ios: "https://apps.apple.com/app/id6596777532",
      android: "https://play.google.com/store/apps/details?id=app.hiddify.com",
      linux: GITHUB.hiddify,
    },
  },
  {
    id: "sing-box",
    name: "sing-box",
    icon: singbox,
    deeplink: (url, name) =>
      `sing-box://import-remote-profile?url=${enc(url)}#${enc(name)}`,
    install: {
      macos: "https://apps.apple.com/app/id6785326793",
      ios: "https://apps.apple.com/app/id6785326793",
      android:
        "https://play.google.com/store/apps/details?id=io.nekohasekai.sfa",
    },
  },
  {
    id: "quantumult-x",
    name: "Quantumult X",
    icon: quantumultx,
    deeplink: (url, name) =>
      `quantumult-x:///add-resource?remote-resource=${enc(JSON.stringify({ server_remote: [`${url}, tag=${name}`] }))}`,
    install: { ios: "https://apps.apple.com/app/id1443988620" },
  },
  {
    id: "v2rayng",
    name: "v2rayNG",
    icon: v2rayng,
    deeplink: (url, name) =>
      `v2rayng://install-sub?url=${enc(url)}&name=${enc(name)}`,
    install: { android: GITHUB.v2rayng },
  },
  {
    id: "v2rayn",
    name: "v2rayN",
    icon: v2rayn,
    deeplink: null,
    install: {
      windows: GITHUB.v2rayn,
      linux: GITHUB.v2rayn,
      macos: GITHUB.v2rayn,
    },
  },
];

export function appsFor(platform: Platform): ConnectApp[] {
  return [PROXIMA_APP, ...THIRD_PARTY_APPS].filter(
    (app) => platform in app.install,
  );
}

/** Best guess of the visitor's OS; desktop Safari on iPad reports as macOS with touch. */
export function detectPlatform(): Platform {
  const ua = navigator.userAgent;
  if (/android/i.test(ua)) return "android";
  if (
    /iphone|ipad|ipod/i.test(ua) ||
    (/macintosh/i.test(ua) && navigator.maxTouchPoints > 1)
  )
    return "ios";
  if (/windows/i.test(ua)) return "windows";
  if (/macintosh|mac os x/i.test(ua)) return "macos";
  if (/linux|x11/i.test(ua)) return "linux";
  return "windows";
}
