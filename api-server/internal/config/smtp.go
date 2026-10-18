package config

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// SMTP transport security modes accepted by SMTPConfig.TLS.
const (
	// SMTPTLSStartTLS upgrades a plaintext connection with STARTTLS (usually
	// port 587) and refuses to continue when the server does not offer it.
	SMTPTLSStartTLS = "starttls"
	// SMTPTLSImplicit wraps the whole connection in TLS (usually port 465).
	SMTPTLSImplicit = "tls"
	// SMTPTLSNone is plaintext. Credentials are never sent in this mode.
	SMTPTLSNone = "none"

	// DefaultSMTPPort is the submission port used when none is configured.
	DefaultSMTPPort = 587
)

// SMTPConfig describes the outgoing mail server used for password reset email.
// Mail is enabled if and only if Host is set; everything else is then checked
// by Validate, and an invalid configuration leaves mail disabled rather than
// preventing the API from starting.
type SMTPConfig struct {
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	Username string `yaml:"username" json:"username"`
	// Password is environment-only (SMTP_PASSWORD), like the Cloudflare token,
	// and a Secret so it can never reach logs, JSON or YAML output; only
	// SMTPPassword hands the plaintext to the mailer.
	Password Secret `yaml:"-" json:"-"`
	// From is the RFC 5322 sender, e.g. `Proxima <no-reply@example.com>`.
	From string `yaml:"from" json:"from"`
	// TLS is one of starttls (default), tls or none.
	TLS string `yaml:"tls" json:"tls"`
}

// SMTPPassword is the only plaintext escape hatch, for mailer wiring.
func (c SMTPConfig) SMTPPassword() string { return c.Password.value }

// Enabled reports whether password reset email can be sent: a host is set and
// the rest of the configuration is valid.
func (c SMTPConfig) Enabled() bool {
	return strings.TrimSpace(c.Host) != "" && c.Validate() == nil
}

// normalize trims and lower-cases the values that are compared literally.
func (c *SMTPConfig) normalize() {
	c.Host = strings.TrimSpace(c.Host)
	c.Username = strings.TrimSpace(c.Username)
	c.From = strings.TrimSpace(c.From)
	c.TLS = strings.ToLower(strings.TrimSpace(c.TLS))
	if c.TLS == "" {
		c.TLS = SMTPTLSStartTLS
	}
}

// Validate returns nil for an unset host (mail simply disabled) or a complete
// configuration, and a descriptive error for a half-configured one.
func (c SMTPConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return nil
	}
	if strings.ContainsFunc(c.Host, func(r rune) bool { return r <= ' ' || r == 0x7f || r == '/' }) {
		return errors.New("SMTP_HOST must be a bare hostname or IP address")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("SMTP_PORT must be a number between 1 and 65535")
	}
	switch c.TLS {
	case SMTPTLSStartTLS, SMTPTLSImplicit, SMTPTLSNone:
	default:
		return fmt.Errorf("SMTP_TLS must be one of %s, %s, %s", SMTPTLSStartTLS, SMTPTLSImplicit, SMTPTLSNone)
	}
	if c.From == "" {
		return errors.New("SMTP_FROM is required when SMTP_HOST is set")
	}
	if strings.ContainsFunc(c.From, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return errors.New("SMTP_FROM must not contain control characters")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return errors.New("SMTP_FROM must be a valid address such as Proxima <no-reply@example.com>")
	}
	if (c.Username == "") != (c.Password.value == "") {
		return errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together")
	}
	return nil
}
