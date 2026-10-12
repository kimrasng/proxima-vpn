package config_test

import (
	"path/filepath"
	"strings"
	"testing"
)

const testZoneID = "0123456789abcdef0123456789abcdef"

func clearManagedDNSEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	t.Setenv("CLOUDFLARE_ZONE_ID", "")
	t.Setenv("ENTRY_DNS_BASE_DOMAIN", "")
	t.Setenv("CLOUDFLARE_API_BASE_URL", "")
}

func TestLoad_ManagedEntryDNSInactive_whenAllInputsAbsent(t *testing.T) {
	// Given
	clearManagedDNSEnv(t)

	// When
	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

	// Then
	if cfg.ManagedEntryDNS.Active() || cfg.ManagedEntryDNS.ValidationError() != nil {
		t.Fatalf("absent DNS settings should be inactive without an error: %v", cfg.ManagedEntryDNS)
	}
}

func TestLoad_ManagedEntryDNSActive_whenYAMLAndEnvironmentComplete(t *testing.T) {
	// Given
	clearManagedDNSEnv(t)
	path := writeConfig(t, "managed_entry_dns:\n  zone_id: "+testZoneID+"\n  base_domain: Nodes.Example.COM.\n")
	t.Setenv("CLOUDFLARE_API_TOKEN", "private-token")

	// When
	cfg := loadConfig(t, path)

	// Then
	if !cfg.ManagedEntryDNS.Active() || cfg.ManagedEntryDNS.ValidationError() != nil {
		t.Fatalf("complete DNS settings should be active: %v", cfg.ManagedEntryDNS.ValidationError())
	}
	if cfg.ManagedEntryDNS.ZoneID != testZoneID || cfg.ManagedEntryDNS.BaseDomain != "nodes.example.com" {
		t.Errorf("DNS settings = %q, %q", cfg.ManagedEntryDNS.ZoneID, cfg.ManagedEntryDNS.BaseDomain)
	}
	if got := cfg.ManagedEntryDNS.CloudflareToken(); got != "private-token" {
		t.Error("explicit client accessor did not return the token")
	}
}

func TestLoad_ManagedEntryDNSEnvOverridesYAML_whenBothSet(t *testing.T) {
	// Given
	clearManagedDNSEnv(t)
	path := writeConfig(t, "managed_entry_dns:\n  zone_id: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n  base_domain: yaml.example.com\n")
	t.Setenv("CLOUDFLARE_ZONE_ID", testZoneID)
	t.Setenv("ENTRY_DNS_BASE_DOMAIN", "ENV.Example.COM.")
	t.Setenv("CLOUDFLARE_API_TOKEN", "env-token")

	// When
	cfg := loadConfig(t, path)

	// Then
	if !cfg.ManagedEntryDNS.Active() || cfg.ManagedEntryDNS.ZoneID != testZoneID || cfg.ManagedEntryDNS.BaseDomain != "env.example.com" {
		t.Errorf("environment did not override YAML: %v", cfg.ManagedEntryDNS)
	}
}

func TestLoad_ManagedEntryDNSInactive_whenInputsPartial(t *testing.T) {
	tests := []struct {
		name, token, zone, base string
	}{
		{"token only", "private-token", "", ""},
		{"zone only", "", testZoneID, ""},
		{"base only", "", "", "example.com"},
		{"missing token", "", testZoneID, "example.com"},
		{"missing zone", "private-token", "", "example.com"},
		{"missing base", "private-token", testZoneID, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			clearManagedDNSEnv(t)
			t.Setenv("CLOUDFLARE_API_TOKEN", tt.token)
			t.Setenv("CLOUDFLARE_ZONE_ID", tt.zone)
			t.Setenv("ENTRY_DNS_BASE_DOMAIN", tt.base)

			// When
			cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

			// Then
			if cfg.ManagedEntryDNS.Active() || cfg.ManagedEntryDNS.ValidationError() == nil {
				t.Errorf("partial DNS settings should be inactive with a diagnostic")
			}
			if err := cfg.ManagedEntryDNS.ValidationError(); err != nil && strings.Contains(err.Error(), "private-token") {
				t.Error("diagnostic exposed the token")
			}
		})
	}
}

func TestLoad_ManagedEntryDNSInactive_whenValuesMalformed(t *testing.T) {
	tests := []struct {
		name, zone, base string
	}{
		{"short zone", "1234", "example.com"},
		{"non hex zone", strings.Repeat("g", 32), "example.com"},
		{"scheme", testZoneID, "https://example.com"},
		{"port", testZoneID, "example.com:443"},
		{"wildcard", testZoneID, "*.example.com"},
		{"empty label", testZoneID, "foo..example.com"},
		{"invalid label", testZoneID, "-foo.example.com"},
		{"ip address", testZoneID, "127.0.0.1"},
		{"oversized generated hostname", testZoneID, strings.Repeat("a", 60) + "." + strings.Repeat("b", 60) + "." + strings.Repeat("c", 60) + "." + strings.Repeat("d", 40) + ".com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			clearManagedDNSEnv(t)
			t.Setenv("CLOUDFLARE_API_TOKEN", "private-token")
			t.Setenv("CLOUDFLARE_ZONE_ID", tt.zone)
			t.Setenv("ENTRY_DNS_BASE_DOMAIN", tt.base)

			// When
			cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

			// Then
			if cfg.ManagedEntryDNS.Active() || cfg.ManagedEntryDNS.ValidationError() == nil {
				t.Error("malformed DNS settings should be inactive with a diagnostic")
			}
			if err := cfg.ManagedEntryDNS.ValidationError(); err != nil && (strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), tt.base)) {
				t.Error("diagnostic included raw input")
			}
		})
	}
}

func TestLoad_ManagedEntryDNSInactive_whenYAMLHasOnlyZoneAndBase(t *testing.T) {
	// Given
	clearManagedDNSEnv(t)
	path := writeConfig(t, "managed_entry_dns:\n  zone_id: "+testZoneID+"\n  base_domain: example.com\n")

	// When
	cfg := loadConfig(t, path)

	// Then
	if cfg.ManagedEntryDNS.Active() || cfg.ManagedEntryDNS.CloudflareToken() != "" || cfg.ManagedEntryDNS.ValidationError() == nil {
		t.Error("YAML zone and base without an environment token must stay inactive")
	}
}

// The Cloudflare API override exists for local test stubs. It must never be
// able to send the token anywhere but this host.
func TestLoad_ManagedEntryDNSAPIBaseURLIsLoopbackOnly(t *testing.T) {
	for _, tt := range []struct {
		url    string
		active bool
	}{
		{"http://127.0.0.1:18090/client/v4", true},
		{"http://localhost:18090", true},
		{"https://[::1]:18090", true},
		{"https://api.cloudflare.com/client/v4", false},
		{"http://10.0.0.5:18090", false},
		{"http://user:pass@127.0.0.1:18090", false},
		{"ftp://127.0.0.1", false},
		{"127.0.0.1:18090", false},
	} {
		t.Run(tt.url, func(t *testing.T) {
			// Given
			clearManagedDNSEnv(t)
			t.Setenv("CLOUDFLARE_API_TOKEN", "private-token")
			t.Setenv("CLOUDFLARE_ZONE_ID", testZoneID)
			t.Setenv("ENTRY_DNS_BASE_DOMAIN", "example.com")
			t.Setenv("CLOUDFLARE_API_BASE_URL", tt.url)

			// When
			cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

			// Then
			if cfg.ManagedEntryDNS.Active() != tt.active {
				t.Fatalf("active = %v, want %v (err %v)", cfg.ManagedEntryDNS.Active(), tt.active, cfg.ManagedEntryDNS.ValidationError())
			}
			if tt.active && cfg.ManagedEntryDNS.APIBaseURL != tt.url {
				t.Errorf("APIBaseURL = %q", cfg.ManagedEntryDNS.APIBaseURL)
			}
		})
	}
}
