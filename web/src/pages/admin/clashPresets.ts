import type { ClashSettings } from "../../api/types";

export type ClashPresetId = "recommended" | "privacy" | "compatible" | "lowPower";

// Top-level keys the routing editor writes; mirrors reservedExtraKeys in
// api-server/internal/services/subscription/clash_settings.go.
export const RESERVED_EXTRA_KEYS = ["proxies", "proxy-groups", "rule-providers", "rules", "proxy-providers"];

export const DNS_SERVER_SUGGESTIONS = [
  { key: "cloudflare", value: "https://cloudflare-dns.com/dns-query" },
  { key: "google", value: "https://dns.google/dns-query" },
  { key: "quad9", value: "https://dns.quad9.net/dns-query" },
  { key: "adguard", value: "https://dns.adguard-dns.com/dns-query" },
] as const;

export const EXTRA_YAML_SNIPPETS = [
  { key: "tun", yaml: "tun:\n  enable: false\n  stack: mixed\n  auto-route: true\n  auto-detect-interface: true" },
  { key: "ntp", yaml: "ntp:\n  enable: true\n  server: time.apple.com\n  port: 123\n  interval: 30" },
  { key: "experimental", yaml: "experimental:\n  quic-go-disable-gso: true" },
  { key: "tcpKeepAlive", yaml: "keep-alive-interval: 30" },
] as const;

const base = (s: ClashSettings): ClashSettings => structuredClone(s);

export const CLASH_PRESETS: Record<ClashPresetId, { apply: (s: ClashSettings) => ClashSettings }> = {
  recommended: {
    apply: (cur) => {
      const s = base(cur);
      Object.assign(s.general, {
        mixed_port: 7890, port: 0, socks_port: 0, mode: "rule", log_level: "warning",
        allow_lan: false, ipv6: false, unified_delay: true, tcp_concurrent: true,
        find_process_mode: "off", store_selected: true, store_fake_ip: true,
      });
      Object.assign(s.dns, {
        enable: true, enhanced_mode: "fake-ip", fake_ip_range: "198.18.0.1/16", fake_ip_filter_mode: "rule",
        respect_rules: true, ipv6: false,
        default_nameserver: ["1.1.1.1", "8.8.8.8"],
        nameserver: ["https://cloudflare-dns.com/dns-query", "https://dns.google/dns-query"],
        proxy_server_nameserver: ["https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"],
        direct_nameserver: ["system"],
      });
      Object.assign(s.sniffer, {
        enable: true, force_dns_mapping: true, parse_pure_ip: true,
        http_ports: ["80", "8080-8880"], tls_ports: ["443", "8443"],
      });
      return s;
    },
  },
  privacy: {
    apply: (cur) => {
      const s = CLASH_PRESETS.recommended.apply(cur);
      Object.assign(s.general, { allow_lan: false, external_controller: "", secret: "", log_level: "silent" });
      Object.assign(s.dns, {
        nameserver: ["https://dns.quad9.net/dns-query", "https://cloudflare-dns.com/dns-query"],
        proxy_server_nameserver: ["https://dns.quad9.net/dns-query"],
        direct_nameserver: ["https://dns.quad9.net/dns-query"],
        respect_rules: true, use_system_hosts: false,
      });
      return s;
    },
  },
  compatible: {
    apply: (cur) => {
      const s = CLASH_PRESETS.recommended.apply(cur);
      Object.assign(s.dns, { enhanced_mode: "redir-host", ipv6: false });
      Object.assign(s.sniffer, { enable: false });
      Object.assign(s.general, { tcp_concurrent: false, unified_delay: false });
      return s;
    },
  },
  lowPower: {
    apply: (cur) => {
      const s = CLASH_PRESETS.recommended.apply(cur);
      Object.assign(s.general, {
        keep_alive_interval: 60, keep_alive_idle: 600, tcp_concurrent: false,
        log_level: "error", find_process_mode: "off", geo_auto_update: false,
      });
      return s;
    },
  },
};
