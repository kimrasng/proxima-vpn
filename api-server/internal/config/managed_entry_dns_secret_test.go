package config_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoad_ManagedEntryDNSTokenRedacted_whenFormattedOrMarshaled(t *testing.T) {
	// Given
	clearManagedDNSEnv(t)
	t.Setenv("CLOUDFLARE_API_TOKEN", "highly-private-token")
	t.Setenv("CLOUDFLARE_ZONE_ID", testZoneID)
	t.Setenv("ENTRY_DNS_BASE_DOMAIN", "example.com")

	// When
	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))
	jsonBytes, jsonErr := json.Marshal(cfg)
	yamlBytes, yamlErr := yaml.Marshal(cfg)

	// Then
	if jsonErr != nil || yamlErr != nil {
		t.Fatalf("marshal config: JSON %v, YAML %v", jsonErr, yamlErr)
	}
	for name, output := range map[string]string{
		"fmt default": fmt.Sprint(cfg),
		"fmt verbose": fmt.Sprintf("%+v", cfg),
		"fmt Go":      fmt.Sprintf("%#v", cfg),
		"JSON":        string(jsonBytes),
		"YAML":        string(yamlBytes),
	} {
		if strings.Contains(output, "highly-private-token") {
			t.Errorf("%s disclosed token", name)
		}
	}
	if got := cfg.ManagedEntryDNS.CloudflareToken(); got != "highly-private-token" {
		t.Error("explicit client accessor should retain the secret")
	}
}

func TestLoad_ManagedEntryDNSSecretRedacted_whenFormattedOrMarshaled(t *testing.T) {
	// Given
	clearManagedDNSEnv(t)
	t.Setenv("CLOUDFLARE_API_TOKEN", "highly-private-token")
	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))
	secret := cfg.ManagedEntryDNS.Token

	// When
	jsonBytes, jsonErr := json.Marshal(secret)
	yamlBytes, yamlErr := yaml.Marshal(secret)

	// Then
	if jsonErr != nil || yamlErr != nil {
		t.Fatalf("marshal secret: JSON %v, YAML %v", jsonErr, yamlErr)
	}
	for name, output := range map[string]string{
		"fmt default": fmt.Sprint(secret),
		"fmt verbose": fmt.Sprintf("%+v", secret),
		"fmt Go":      fmt.Sprintf("%#v", secret),
		"JSON":        string(jsonBytes),
		"YAML":        string(yamlBytes),
	} {
		if strings.Contains(output, "highly-private-token") {
			t.Errorf("%s disclosed token", name)
		}
	}
}
