package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server          ServerConfig          `yaml:"server"`
	Database        DatabaseConfig        `yaml:"database"`
	Redis           RedisConfig           `yaml:"redis"`
	JWT             JWTConfig             `yaml:"jwt"`
	Backup          BackupConfig          `yaml:"backup"`
	Telegram        TelegramConfig        `yaml:"telegram"`
	Subscription    SubscriptionConfig    `yaml:"subscription"`
	Storage         StorageConfig         `yaml:"storage"`
	Payments        PaymentsConfig        `yaml:"payments"`
	ManagedEntryDNS ManagedEntryDNSConfig `yaml:"managed_entry_dns" json:"managed_entry_dns"`
}

// PaymentsConfig governs every payment provider, not just Stripe: the
// currency and pending-order lifetime are panel-wide because plan_prices
// holds bare integer cents with no per-plan currency column.
type PaymentsConfig struct {
	Currency      string       `yaml:"currency"`
	PendingTTLRaw string       `yaml:"pending_ttl"`
	Stripe        StripeConfig `yaml:"stripe"`
}

type StripeConfig struct {
	SecretKey     string `yaml:"secret_key"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// PendingTTL parses PaymentsConfig.PendingTTLRaw, falling back to 24h on an
// unparseable value the same way JWT's AdminExpiry/UserExpiry fall back.
func (p PaymentsConfig) PendingTTL() time.Duration {
	d, err := time.ParseDuration(p.PendingTTLRaw)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}

type StorageConfig struct {
	Type      string   `yaml:"type"`
	LocalPath string   `yaml:"local_path"`
	S3        S3Config `yaml:"s3"`
}

type ServerConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	PanelURL string `yaml:"panel_url"`

	// TrustedProxies lists the addresses and CIDR ranges whose ProxyHeader is
	// believed; any other peer is recorded by its own address. Port 2053 is
	// published alongside nginx, so a client reaching the API directly can
	// send any X-Forwarded-For it likes - without this list every IP-keyed
	// decision (rate limits, login audit rows, payment request addresses) is
	// spoofable. Empty trusts no proxy at all.
	TrustedProxies []string `yaml:"trusted_proxies"`

	// ProxyHeader is where a trusted proxy publishes the real client address.
	// Empty disables forwarded-header handling entirely.
	ProxyHeader string `yaml:"proxy_header"`
}

// TrustNoProxy is the TRUSTED_PROXIES value that clears the list, for a
// deployment with no reverse proxy in front of the API. An empty variable
// cannot mean this: compose passes unset variables through as empty strings,
// which would silently disable forwarding for the standard topology.
const TrustNoProxy = "none"

type DatabaseConfig struct {
	URL            string `yaml:"url"`
	MaxConnections int    `yaml:"max_connections"`
	IdleTimeout    string `yaml:"idle_timeout"`
}

type RedisConfig struct {
	URL string `yaml:"url"`
}

type JWTConfig struct {
	Secret      string `yaml:"secret"`
	AdminExpiry string `yaml:"admin_expiry"`
	UserExpiry  string `yaml:"user_expiry"`
}

type BackupConfig struct {
	S3       S3Config `yaml:"s3"`
	Schedule string   `yaml:"schedule"`
}

type S3Config struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Region    string `yaml:"region"`
}

type TelegramConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

type SubscriptionConfig struct {
	UpdateInterval int `yaml:"update_interval"`
}

// Load reads configuration from a YAML file and applies environment variable overrides.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 2053,
			// The compose topology puts nginx on the bridge network in front
			// of the API, so the private ranges Docker allocates from are the
			// proxies worth believing. Loopback covers running the binary
			// directly behind a host-local proxy.
			TrustedProxies: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.1", "::1"},
			ProxyHeader:    "X-Forwarded-For",
		},
		Database: DatabaseConfig{
			MaxConnections: 20,
			IdleTimeout:    "5m",
		},
		JWT: JWTConfig{
			AdminExpiry: "8h",
			UserExpiry:  "24h",
		},
		Subscription: SubscriptionConfig{
			UpdateInterval: 3600,
		},
		Storage: StorageConfig{
			Type:      "local",
			LocalPath: "/app/uploads",
		},
		Payments: PaymentsConfig{
			Currency:      "usd",
			PendingTTLRaw: "24h",
		},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		// File not found — continue with defaults + env vars
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}
	}

	applyEnvOverrides(cfg)
	cfg.ManagedEntryDNS.resolve()

	if cfg.JWT.Secret == "" {
		// No secret provided: load a persisted one or generate and persist a new
		// random secret. This lets a fresh `git clone && docker compose up` work
		// with zero configuration while keeping the secret out of version control.
		cfg.JWT.Secret = loadOrCreateJWTSecret()
	}

	return cfg, nil
}

// loadOrCreateJWTSecret returns a persisted JWT secret, creating one if needed.
// The path is configurable via JWT_SECRET_FILE (default /app/data/jwt_secret).
// If the secret cannot be persisted, an ephemeral one is returned so the server
// can still start (tokens will invalidate on restart in that case).
func loadOrCreateJWTSecret() string {
	path := os.Getenv("JWT_SECRET_FILE")
	if path == "" {
		path = "/app/data/jwt_secret"
	}

	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("failed to generate JWT secret: %v", err)
	}
	secret := hex.EncodeToString(buf)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if err := os.WriteFile(path, []byte(secret), 0o600); err == nil {
			log.Printf("JWT_SECRET not set; generated and persisted a new secret at %s", path)
			return secret
		}
	}

	log.Println("WARNING: JWT_SECRET not set and could not be persisted; using an ephemeral secret (all tokens invalidate on restart). Set JWT_SECRET for production.")
	return secret
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("CLOUDFLARE_API_TOKEN"); v != "" {
		cfg.ManagedEntryDNS.Token = Secret{value: v}
	}
	if v := os.Getenv("CLOUDFLARE_ZONE_ID"); v != "" {
		cfg.ManagedEntryDNS.ZoneID = v
	}
	if v := os.Getenv("ENTRY_DNS_BASE_DOMAIN"); v != "" {
		cfg.ManagedEntryDNS.BaseDomain = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.Database.URL = v
	}
	if v := os.Getenv("REDIS_URL"); v != "" {
		cfg.Redis.URL = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.JWT.Secret = v
	}
	if v := os.Getenv("PANEL_URL"); v != "" {
		cfg.Server.PanelURL = v
	}
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		cfg.Server.TrustedProxies = parseTrustedProxies(v)
	}
	if v := os.Getenv("PROXY_HEADER"); v != "" {
		cfg.Server.ProxyHeader = v
	}
	if v := os.Getenv("STRIPE_SECRET_KEY"); v != "" {
		cfg.Payments.Stripe.SecretKey = v
	}
	if v := os.Getenv("STRIPE_WEBHOOK_SECRET"); v != "" {
		cfg.Payments.Stripe.WebhookSecret = v
	}
	if v := os.Getenv("PAYMENTS_CURRENCY"); v != "" {
		cfg.Payments.Currency = v
	}
	if v := os.Getenv("PAYMENTS_PENDING_TTL"); v != "" {
		cfg.Payments.PendingTTLRaw = v
	}
}

// parseTrustedProxies splits a comma-separated TRUSTED_PROXIES value. Entries
// stay verbatim so Fiber parses each CIDR with the same code that matches it.
func parseTrustedProxies(raw string) []string {
	if strings.TrimSpace(raw) == TrustNoProxy {
		return nil
	}

	proxies := make([]string, 0, strings.Count(raw, ",")+1)
	for _, entry := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			proxies = append(proxies, trimmed)
		}
	}
	return proxies
}
