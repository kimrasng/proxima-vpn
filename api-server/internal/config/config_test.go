package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
)

// writeConfig writes a config.yaml into a per-test temp dir and returns its
// path. Load treats a missing file as "defaults only", so tests that want the
// defaults simply point Load at a path that was never written.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// loadConfig calls Load with a JWT secret already in the environment so the
// test never touches /app/data/jwt_secret or generates one.
func loadConfig(t *testing.T, path string) *config.Config {
	t.Helper()

	t.Setenv("JWT_SECRET", "test-secret")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// The defaults must match the compose topology: nginx is the only hop in front
// of the API, and it reaches the api service over the compose bridge network,
// so the private ranges Docker allocates from are the trusted set. Anything
// else that finds port 2053 directly is not a proxy and must not be believed.
func TestLoad_ProxyDefaults_whenNoConfigFileOrEnv(t *testing.T) {
	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

	if got, want := cfg.Server.ProxyHeader, "X-Forwarded-For"; got != want {
		t.Errorf("ProxyHeader = %q, want %q", got, want)
	}

	want := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.1", "::1"}
	if len(cfg.Server.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies = %v, want %v", cfg.Server.TrustedProxies, want)
	}
	for i, w := range want {
		if cfg.Server.TrustedProxies[i] != w {
			t.Errorf("TrustedProxies[%d] = %q, want %q", i, cfg.Server.TrustedProxies[i], w)
		}
	}
}

// A deployment behind one known load balancer must be able to narrow the
// trusted set to exactly that host - the YAML list replaces the defaults
// rather than adding to them, or narrowing would be impossible.
func TestLoad_ProxySettingsFromYAML_whenConfigFileSetsThem(t *testing.T) {
	path := writeConfig(t, `
server:
  trusted_proxies:
    - "198.51.100.7"
  proxy_header: "X-Real-IP"
`)

	cfg := loadConfig(t, path)

	if got, want := cfg.Server.ProxyHeader, "X-Real-IP"; got != want {
		t.Errorf("ProxyHeader = %q, want %q", got, want)
	}
	if len(cfg.Server.TrustedProxies) != 1 || cfg.Server.TrustedProxies[0] != "198.51.100.7" {
		t.Errorf("TrustedProxies = %v, want [198.51.100.7]", cfg.Server.TrustedProxies)
	}
}

// TRUSTED_PROXIES/PROXY_HEADER are how a compose deployment overrides the
// settings without baking a config.yaml into the image, so they must win over
// a value the file already set.
func TestLoad_EnvOverridesYAMLProxySettings_whenBothAreSet(t *testing.T) {
	path := writeConfig(t, `
server:
  trusted_proxies:
    - "198.51.100.7"
  proxy_header: "X-Real-IP"
`)
	t.Setenv("TRUSTED_PROXIES", "203.0.113.5, 203.0.113.0/24")
	t.Setenv("PROXY_HEADER", "X-Forwarded-For")

	cfg := loadConfig(t, path)

	if got, want := cfg.Server.ProxyHeader, "X-Forwarded-For"; got != want {
		t.Errorf("ProxyHeader = %q, want %q", got, want)
	}

	want := []string{"203.0.113.5", "203.0.113.0/24"}
	if len(cfg.Server.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies = %v, want %v", cfg.Server.TrustedProxies, want)
	}
	for i, w := range want {
		if cfg.Server.TrustedProxies[i] != w {
			t.Errorf("TrustedProxies[%d] = %q, want %q", i, cfg.Server.TrustedProxies[i], w)
		}
	}
}

// A deployment with no proxy at all (port 2053 published straight to the
// internet) must be able to say "trust nobody". An empty TRUSTED_PROXIES is
// that switch: it has to clear the defaults rather than read as "unset", or
// the only way to opt out would be to list an address that cannot match.
func TestLoad_TrustedProxiesEmptied_whenEnvIsExplicitlyEmptyList(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "none")

	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

	if len(cfg.Server.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want empty", cfg.Server.TrustedProxies)
	}
}
