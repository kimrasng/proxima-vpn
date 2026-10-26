package subscription

import (
	"fmt"
	"net/netip"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClashSettings are the non-routing parts of a Clash Meta profile: general
// options, DNS, sniffer and hosts. ExtraYAML is merged over the generated
// document last, so any mihomo option without a dedicated field (tun,
// listeners, ntp, experimental, ...) can still be set.
type ClashSettings struct {
	General ClashGeneral      `json:"general"`
	DNS     ClashDNSSettings  `json:"dns"`
	Sniffer ClashSniffer      `json:"sniffer"`
	Hosts   map[string]string `json:"hosts"`
	// ExtraYAML is a YAML mapping deep-merged into the profile. It cannot set
	// the keys the routing editor owns.
	ExtraYAML string `json:"extra_yaml"`
}

type ClashGeneral struct {
	MixedPort          int    `json:"mixed_port"`
	Port               int    `json:"port"`
	SocksPort          int    `json:"socks_port"`
	AllowLAN           bool   `json:"allow_lan"`
	BindAddress        string `json:"bind_address"`
	Mode               string `json:"mode"`
	LogLevel           string `json:"log_level"`
	IPv6               bool   `json:"ipv6"`
	UnifiedDelay       bool   `json:"unified_delay"`
	TCPConcurrent      bool   `json:"tcp_concurrent"`
	FindProcessMode    string `json:"find_process_mode"`
	KeepAliveInterval  int    `json:"keep_alive_interval"`
	KeepAliveIdle      int    `json:"keep_alive_idle"`
	DisableKeepAlive   bool   `json:"disable_keep_alive"`
	ExternalController string `json:"external_controller"`
	Secret             string `json:"secret"`
	StoreSelected      bool   `json:"store_selected"`
	StoreFakeIP        bool   `json:"store_fake_ip"`
	GeodataMode        bool   `json:"geodata_mode"`
	GeoAutoUpdate      bool   `json:"geo_auto_update"`
	GeoUpdateInterval  int    `json:"geo_update_interval"`
}

type ClashDNSSettings struct {
	Enable           bool   `json:"enable"`
	Listen           string `json:"listen"`
	IPv6             bool   `json:"ipv6"`
	PreferH3         bool   `json:"prefer_h3"`
	UseHosts         bool   `json:"use_hosts"`
	UseSystemHosts   bool   `json:"use_system_hosts"`
	CacheAlgorithm   string `json:"cache_algorithm"`
	EnhancedMode     string `json:"enhanced_mode"`
	FakeIPRange      string `json:"fake_ip_range"`
	FakeIPFilterMode string `json:"fake_ip_filter_mode"`
	// FakeIPFilter empty in rule mode means "LAN names real, everything else
	// fake", derived from the private_domain rule set when present.
	FakeIPFilter                 []string            `json:"fake_ip_filter"`
	RespectRules                 bool                `json:"respect_rules"`
	DefaultNameserver            []string            `json:"default_nameserver"`
	Nameserver                   []string            `json:"nameserver"`
	Fallback                     []string            `json:"fallback"`
	ProxyServerNameserver        []string            `json:"proxy_server_nameserver"`
	DirectNameserver             []string            `json:"direct_nameserver"`
	DirectNameserverFollowPolicy bool                `json:"direct_nameserver_follow_policy"`
	NameserverPolicy             map[string][]string `json:"nameserver_policy"`
	FallbackFilter               ClashFallbackFilter `json:"fallback_filter"`
}

type ClashFallbackFilter struct {
	GeoIP     bool     `json:"geoip"`
	GeoIPCode string   `json:"geoip_code"`
	IPCIDR    []string `json:"ipcidr"`
	Domain    []string `json:"domain"`
}

type ClashSniffer struct {
	Enable              bool     `json:"enable"`
	ForceDNSMapping     bool     `json:"force_dns_mapping"`
	ParsePureIP         bool     `json:"parse_pure_ip"`
	OverrideDestination bool     `json:"override_destination"`
	HTTPPorts           []string `json:"http_ports"`
	HTTPOverride        bool     `json:"http_override_destination"`
	TLSPorts            []string `json:"tls_ports"`
	QUICPorts           []string `json:"quic_ports"`
	SkipDomain          []string `json:"skip_domain"`
	ForceDomain         []string `json:"force_domain"`
	SkipSrcAddress      []string `json:"skip_src_address"`
	SkipDstAddress      []string `json:"skip_dst_address"`
}

// DefaultClashSettings mirror what the generator emitted before settings were
// editable: rule mode, fake-ip with rule-based filtering, DoH upstreams, and
// HTTP/TLS sniffing without QUIC (UDP 443 is rejected by routing).
func DefaultClashSettings() ClashSettings {
	return ClashSettings{
		General: ClashGeneral{
			MixedPort: 7890, AllowLAN: true, BindAddress: "*", Mode: "rule", LogLevel: "warning",
			UnifiedDelay: true, TCPConcurrent: true, FindProcessMode: "off",
			ExternalController: "127.0.0.1:9090", StoreSelected: true, StoreFakeIP: true,
		},
		DNS: ClashDNSSettings{
			Enable: true, Listen: "0.0.0.0:1053", EnhancedMode: "fake-ip", FakeIPRange: "198.18.0.1/16",
			FakeIPFilterMode: "rule", RespectRules: true, UseHosts: true,
			DefaultNameserver:     []string{"1.1.1.1", "8.8.8.8"},
			Nameserver:            []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/dns-query"},
			ProxyServerNameserver: []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
			DirectNameserver:      []string{"system"},
		},
		Sniffer: ClashSniffer{
			Enable: true, ForceDNSMapping: true, ParsePureIP: true,
			HTTPPorts: []string{"80", "8080-8880"}, TLSPorts: []string{"443", "8443"},
			SkipDomain: []string{"+.push.apple.com"},
		},
	}
}

var (
	clashModes        = map[string]bool{"rule": true, "global": true, "direct": true}
	clashLogLevels    = map[string]bool{"silent": true, "error": true, "warning": true, "info": true, "debug": true}
	findProcessModes  = map[string]bool{"off": true, "strict": true, "always": true}
	enhancedModes     = map[string]bool{"fake-ip": true, "redir-host": true, "normal": true}
	fakeIPFilterModes = map[string]bool{"blacklist": true, "whitelist": true, "rule": true}
	cacheAlgorithms   = map[string]bool{"": true, "lru": true, "arc": true}
	// reservedExtraKeys are written by the routing editor; extra YAML must not
	// fight it for ownership.
	reservedExtraKeys = map[string]bool{"proxies": true, "proxy-groups": true, "rule-providers": true, "rules": true, "proxy-providers": true}
)

func validateSettings(s ClashSettings, add func(path, format string, args ...any)) {
	g := s.General
	for name, port := range map[string]int{"mixed_port": g.MixedPort, "port": g.Port, "socks_port": g.SocksPort} {
		if port < 0 || port > 65535 {
			add("settings.general."+name, "port must be 0-65535")
		}
	}
	if !clashModes[g.Mode] {
		add("settings.general.mode", "mode must be rule, global or direct")
	}
	if !clashLogLevels[g.LogLevel] {
		add("settings.general.log_level", "log level must be silent, error, warning, info or debug")
	}
	if !findProcessModes[g.FindProcessMode] {
		add("settings.general.find_process_mode", "find-process-mode must be off, strict or always")
	}
	for name, v := range map[string]int{"keep_alive_interval": g.KeepAliveInterval, "keep_alive_idle": g.KeepAliveIdle, "geo_update_interval": g.GeoUpdateInterval} {
		if v < 0 || v > 86400*30 {
			add("settings.general."+name, "value must be 0-2592000")
		}
	}
	for name, v := range map[string]string{"bind_address": g.BindAddress, "external_controller": g.ExternalController, "secret": g.Secret} {
		if strings.ContainsAny(v, "\n\r") {
			add("settings.general."+name, "value must be on one line")
		}
	}

	d := s.DNS
	if !enhancedModes[d.EnhancedMode] {
		add("settings.dns.enhanced_mode", "enhanced-mode must be fake-ip, redir-host or normal")
	}
	if !fakeIPFilterModes[d.FakeIPFilterMode] {
		add("settings.dns.fake_ip_filter_mode", "fake-ip-filter-mode must be blacklist, whitelist or rule")
	}
	if !cacheAlgorithms[d.CacheAlgorithm] {
		add("settings.dns.cache_algorithm", "cache-algorithm must be lru or arc")
	}
	if d.EnhancedMode == "fake-ip" {
		if _, err := netip.ParsePrefix(d.FakeIPRange); err != nil {
			add("settings.dns.fake_ip_range", "fake-ip-range must be a CIDR like 198.18.0.1/16")
		}
	}
	if d.Enable && len(d.Nameserver) == 0 {
		add("settings.dns.nameserver", "at least one nameserver is required when DNS is enabled")
	}
	if d.RespectRules && len(d.ProxyServerNameserver) == 0 {
		add("settings.dns.proxy_server_nameserver", "respect-rules needs proxy-server-nameserver")
	}
	for name, list := range map[string][]string{
		"default_nameserver": d.DefaultNameserver, "nameserver": d.Nameserver, "fallback": d.Fallback,
		"proxy_server_nameserver": d.ProxyServerNameserver, "direct_nameserver": d.DirectNameserver,
		"fake_ip_filter": d.FakeIPFilter,
	} {
		checkLines("settings.dns."+name, list, add)
	}
	for _, v := range d.DefaultNameserver {
		host := strings.TrimPrefix(strings.TrimPrefix(v, "udp://"), "tcp://")
		if h, _, err := splitHostPortLoose(host); err != nil || !isIP(h) {
			add("settings.dns.default_nameserver", "default-nameserver entries must be plain IP addresses, got %q", v)
		}
	}
	for k, v := range d.NameserverPolicy {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, "\n\r") || len(v) == 0 {
			add("settings.dns.nameserver_policy", "each policy needs a pattern and at least one server")
		}
		checkLines("settings.dns.nameserver_policy", v, add)
	}
	checkLines("settings.dns.fallback_filter.ipcidr", d.FallbackFilter.IPCIDR, add)
	checkLines("settings.dns.fallback_filter.domain", d.FallbackFilter.Domain, add)

	sn := s.Sniffer
	for name, list := range map[string][]string{"http_ports": sn.HTTPPorts, "tls_ports": sn.TLSPorts, "quic_ports": sn.QUICPorts} {
		for _, p := range list {
			if !validPortSpec(p) {
				add("settings.sniffer."+name, "port %q must be a port or range like 8080-8880", p)
			}
		}
	}
	for name, list := range map[string][]string{"skip_domain": sn.SkipDomain, "force_domain": sn.ForceDomain, "skip_src_address": sn.SkipSrcAddress, "skip_dst_address": sn.SkipDstAddress} {
		checkLines("settings.sniffer."+name, list, add)
	}

	for k, v := range s.Hosts {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k+v, "\n\r") || strings.TrimSpace(v) == "" {
			add("settings.hosts", "each host needs a name and a value")
		}
	}

	if strings.TrimSpace(s.ExtraYAML) != "" {
		var extra map[string]any
		if err := yaml.Unmarshal([]byte(s.ExtraYAML), &extra); err != nil {
			add("settings.extra_yaml", "extra YAML must be a mapping: %v", err)
		} else {
			for k := range extra {
				if reservedExtraKeys[k] {
					add("settings.extra_yaml", "%q is managed by the routing editor", k)
				}
			}
		}
	}
}

func checkLines(path string, list []string, add func(path, format string, args ...any)) {
	for _, v := range list {
		if strings.TrimSpace(v) == "" || strings.ContainsAny(v, "\n\r") {
			add(path, "entries must be non-empty single lines")
			return
		}
	}
}

func isIP(s string) bool {
	_, err := netip.ParseAddr(strings.Trim(s, "[]"))
	return err == nil
}

func splitHostPortLoose(s string) (string, string, error) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", fmt.Errorf("bad address")
		}
		return s[1:end], strings.TrimPrefix(s[end+1:], ":"), nil
	}
	if strings.Count(s, ":") == 1 {
		h, p, _ := strings.Cut(s, ":")
		return h, p, nil
	}
	return s, "", nil
}

func nonEmpty[T any](v []T) []T {
	if len(v) == 0 {
		return nil
	}
	return v
}

// settingsDocument renders the settings as the top-level Clash keys they map to.
func settingsDocument(s ClashSettings, fakeIPFilter []string) map[string]any {
	g := s.General
	doc := map[string]any{
		"allow-lan":         g.AllowLAN,
		"bind-address":      g.BindAddress,
		"mode":              g.Mode,
		"log-level":         g.LogLevel,
		"ipv6":              g.IPv6,
		"unified-delay":     g.UnifiedDelay,
		"tcp-concurrent":    g.TCPConcurrent,
		"find-process-mode": g.FindProcessMode,
		"profile":           map[string]any{"store-selected": g.StoreSelected, "store-fake-ip": g.StoreFakeIP},
	}
	setIf := func(key string, v any, ok bool) {
		if ok {
			doc[key] = v
		}
	}
	setIf("mixed-port", g.MixedPort, g.MixedPort > 0)
	setIf("port", g.Port, g.Port > 0)
	setIf("socks-port", g.SocksPort, g.SocksPort > 0)
	setIf("keep-alive-interval", g.KeepAliveInterval, g.KeepAliveInterval > 0)
	setIf("keep-alive-idle", g.KeepAliveIdle, g.KeepAliveIdle > 0)
	setIf("disable-keep-alive", true, g.DisableKeepAlive)
	setIf("external-controller", g.ExternalController, g.ExternalController != "")
	setIf("secret", g.Secret, g.Secret != "")
	setIf("geodata-mode", true, g.GeodataMode)
	setIf("geo-auto-update", true, g.GeoAutoUpdate)
	setIf("geo-update-interval", g.GeoUpdateInterval, g.GeoUpdateInterval > 0)

	d := s.DNS
	dns := map[string]any{
		"enable":        d.Enable,
		"ipv6":          d.IPv6,
		"enhanced-mode": d.EnhancedMode,
		"respect-rules": d.RespectRules,
		"use-hosts":     d.UseHosts,
	}
	put := func(m map[string]any, key string, v any, ok bool) {
		if ok {
			m[key] = v
		}
	}
	put(dns, "listen", d.Listen, d.Listen != "")
	put(dns, "prefer-h3", true, d.PreferH3)
	put(dns, "use-system-hosts", d.UseSystemHosts, d.UseSystemHosts)
	put(dns, "cache-algorithm", d.CacheAlgorithm, d.CacheAlgorithm != "")
	if d.EnhancedMode == "fake-ip" {
		dns["fake-ip-range"] = d.FakeIPRange
		dns["fake-ip-filter-mode"] = d.FakeIPFilterMode
		filter := d.FakeIPFilter
		if len(filter) == 0 && d.FakeIPFilterMode == "rule" {
			filter = fakeIPFilter
		}
		put(dns, "fake-ip-filter", filter, len(filter) > 0)
	}
	put(dns, "default-nameserver", d.DefaultNameserver, len(d.DefaultNameserver) > 0)
	put(dns, "nameserver", d.Nameserver, len(d.Nameserver) > 0)
	put(dns, "fallback", d.Fallback, len(d.Fallback) > 0)
	put(dns, "proxy-server-nameserver", d.ProxyServerNameserver, len(d.ProxyServerNameserver) > 0)
	put(dns, "direct-nameserver", d.DirectNameserver, len(d.DirectNameserver) > 0)
	put(dns, "direct-nameserver-follow-policy", true, d.DirectNameserverFollowPolicy)
	put(dns, "nameserver-policy", d.NameserverPolicy, len(d.NameserverPolicy) > 0)
	ff := d.FallbackFilter
	if len(d.Fallback) > 0 && (ff.GeoIP || len(ff.IPCIDR) > 0 || len(ff.Domain) > 0) {
		filter := map[string]any{"geoip": ff.GeoIP}
		put(filter, "geoip-code", ff.GeoIPCode, ff.GeoIPCode != "")
		put(filter, "ipcidr", ff.IPCIDR, len(ff.IPCIDR) > 0)
		put(filter, "domain", ff.Domain, len(ff.Domain) > 0)
		dns["fallback-filter"] = filter
	}
	doc["dns"] = dns

	sn := s.Sniffer
	sniff := map[string]any{}
	if len(sn.HTTPPorts) > 0 {
		h := map[string]any{"ports": sn.HTTPPorts}
		put(h, "override-destination", true, sn.HTTPOverride)
		sniff["HTTP"] = h
	}
	put(sniff, "TLS", map[string]any{"ports": sn.TLSPorts}, len(sn.TLSPorts) > 0)
	put(sniff, "QUIC", map[string]any{"ports": sn.QUICPorts}, len(sn.QUICPorts) > 0)
	sniffer := map[string]any{
		"enable":               sn.Enable,
		"force-dns-mapping":    sn.ForceDNSMapping,
		"parse-pure-ip":        sn.ParsePureIP,
		"override-destination": sn.OverrideDestination,
	}
	put(sniffer, "sniff", sniff, len(sniff) > 0)
	put(sniffer, "skip-domain", nonEmpty(sn.SkipDomain), len(sn.SkipDomain) > 0)
	put(sniffer, "force-domain", nonEmpty(sn.ForceDomain), len(sn.ForceDomain) > 0)
	put(sniffer, "skip-src-address", nonEmpty(sn.SkipSrcAddress), len(sn.SkipSrcAddress) > 0)
	put(sniffer, "skip-dst-address", nonEmpty(sn.SkipDstAddress), len(sn.SkipDstAddress) > 0)
	doc["sniffer"] = sniffer

	if len(s.Hosts) > 0 {
		doc["hosts"] = s.Hosts
	}
	return doc
}

// mergeYAML deep-merges src into dst: mappings merge key by key, anything else
// (scalars, lists) replaces the destination value.
func mergeYAML(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeYAML(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}
