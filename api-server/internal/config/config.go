package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Database     DatabaseConfig     `yaml:"database"`
	Redis        RedisConfig        `yaml:"redis"`
	JWT          JWTConfig          `yaml:"jwt"`
	Backup       BackupConfig       `yaml:"backup"`
	Telegram     TelegramConfig     `yaml:"telegram"`
	Subscription SubscriptionConfig `yaml:"subscription"`
	Storage      StorageConfig      `yaml:"storage"`
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
}

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
}
