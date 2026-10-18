package config_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/proximavpn/proxima-vpn/api-server/internal/config"
	"gopkg.in/yaml.v3"
)

func clearSMTPEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM", "SMTP_TLS"} {
		t.Setenv(k, "")
	}
}

func TestLoad_SMTPDisabledWithDefaults_whenHostUnset(t *testing.T) {
	clearSMTPEnv(t)

	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

	if cfg.SMTP.Enabled() {
		t.Error("SMTP enabled without a host")
	}
	if err := cfg.SMTP.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil when mail is simply unconfigured", err)
	}
	if cfg.SMTP.Port != 587 || cfg.SMTP.TLS != config.SMTPTLSStartTLS {
		t.Errorf("defaults = port %d tls %q, want 587 starttls", cfg.SMTP.Port, cfg.SMTP.TLS)
	}
}

func TestLoad_SMTPFromEnvironment(t *testing.T) {
	clearSMTPEnv(t)
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "s3cret-password")
	t.Setenv("SMTP_FROM", "Proxima <no-reply@example.com>")
	t.Setenv("SMTP_TLS", " TLS ")

	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

	s := cfg.SMTP
	if s.Host != "smtp.example.com" || s.Port != 465 || s.Username != "mailer" ||
		s.From != "Proxima <no-reply@example.com>" || s.TLS != config.SMTPTLSImplicit {
		t.Errorf("SMTP = %+v", s)
	}
	if s.SMTPPassword() != "s3cret-password" {
		t.Error("SMTPPassword() lost the password")
	}
	if !s.Enabled() {
		t.Errorf("Enabled() = false, Validate() = %v", s.Validate())
	}
}

func TestLoad_SMTPEnvironmentOverridesYAML(t *testing.T) {
	clearSMTPEnv(t)
	path := writeConfig(t, `
smtp:
  host: yaml.example.com
  port: 25
  from: "YAML <yaml@example.com>"
  tls: none
`)
	t.Setenv("SMTP_HOST", "env.example.com")
	t.Setenv("SMTP_TLS", "starttls")

	cfg := loadConfig(t, path)

	if cfg.SMTP.Host != "env.example.com" || cfg.SMTP.TLS != config.SMTPTLSStartTLS {
		t.Errorf("env did not win: %+v", cfg.SMTP)
	}
	if cfg.SMTP.Port != 25 || cfg.SMTP.From != "YAML <yaml@example.com>" {
		t.Errorf("YAML values lost where env is unset: %+v", cfg.SMTP)
	}
}

func TestSMTPValidate(t *testing.T) {
	t.Setenv("JWT_SECRET", "x")
	valid := func() config.SMTPConfig {
		clearSMTPEnv(t)
		t.Setenv("SMTP_HOST", "smtp.example.com")
		t.Setenv("SMTP_FROM", "Proxima <no-reply@example.com>")
		return loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml")).SMTP
	}

	if err := valid().Validate(); err != nil {
		t.Fatalf("baseline invalid: %v", err)
	}

	cases := map[string]func(c *config.SMTPConfig){
		"port zero":        func(c *config.SMTPConfig) { c.Port = 0 },
		"port too large":   func(c *config.SMTPConfig) { c.Port = 70000 },
		"unknown tls mode": func(c *config.SMTPConfig) { c.TLS = "ssl" },
		"missing from":     func(c *config.SMTPConfig) { c.From = "" },
		"from not address": func(c *config.SMTPConfig) { c.From = "Proxima" },
		"from with CRLF":   func(c *config.SMTPConfig) { c.From = "A <a@example.com>\r\nBcc: b@example.com" },
		"host with space":  func(c *config.SMTPConfig) { c.Host = "smtp example.com" },
		"host with slash":  func(c *config.SMTPConfig) { c.Host = "smtp.example.com/x" },
		"username only":    func(c *config.SMTPConfig) { c.Username = "mailer" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("Validate() accepted %+v", c)
			}
			if c.Enabled() {
				t.Error("Enabled() true for an invalid configuration")
			}
		})
	}
}

func TestLoad_SMTPUnparseablePortIsReported(t *testing.T) {
	clearSMTPEnv(t)
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_FROM", "Proxima <no-reply@example.com>")
	t.Setenv("SMTP_PORT", "five-eighty-seven")

	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))

	if err := cfg.SMTP.Validate(); err == nil || !strings.Contains(err.Error(), "SMTP_PORT") {
		t.Errorf("Validate() = %v, want an SMTP_PORT error rather than a silent default", err)
	}
}

func TestLoad_SMTPPasswordRedacted_whenFormattedOrMarshaled(t *testing.T) {
	clearSMTPEnv(t)
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "very-private-smtp-password")
	t.Setenv("SMTP_FROM", "Proxima <no-reply@example.com>")

	cfg := loadConfig(t, filepath.Join(t.TempDir(), "missing.yaml"))
	jsonBytes, jsonErr := json.Marshal(cfg)
	yamlBytes, yamlErr := yaml.Marshal(cfg)
	if jsonErr != nil || yamlErr != nil {
		t.Fatalf("marshal config: JSON %v, YAML %v", jsonErr, yamlErr)
	}
	for name, output := range map[string]string{
		"fmt default":     fmt.Sprint(cfg),
		"fmt verbose":     fmt.Sprintf("%+v", cfg),
		"fmt Go":          fmt.Sprintf("%#v", cfg),
		"fmt SMTP struct": fmt.Sprintf("%+v %#v", cfg.SMTP, cfg.SMTP),
		"JSON":            string(jsonBytes),
		"YAML":            string(yamlBytes),
	} {
		if strings.Contains(output, "very-private-smtp-password") {
			t.Errorf("%s disclosed the SMTP password", name)
		}
	}
	if cfg.SMTP.SMTPPassword() != "very-private-smtp-password" {
		t.Error("explicit accessor should retain the secret")
	}
}

func TestLoad_SMTPPasswordIgnoredInYAML(t *testing.T) {
	clearSMTPEnv(t)
	path := writeConfig(t, `
smtp:
  host: smtp.example.com
  from: "Proxima <no-reply@example.com>"
  username: mailer
  password: from-yaml
`)

	cfg := loadConfig(t, path)

	if cfg.SMTP.SMTPPassword() != "" {
		t.Error("password was read from YAML; like the other credentials it is environment-only")
	}
}
