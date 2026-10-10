package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var (
	ErrManagedEntryDNSIncomplete = errors.New("managed entry DNS requires Cloudflare token, zone ID, and base domain")
	ErrManagedEntryDNSZoneID     = errors.New("managed entry DNS zone ID must be 32 hex characters")
	ErrManagedEntryDNSBaseDomain = errors.New("managed entry DNS base domain is not a valid DNS hostname")
	ErrManagedEntryDNSToken      = errors.New("managed entry DNS token must not be blank")
)

// Secret holds environment-only credentials. Formatting and serialization never
// expose the underlying value; only explicit client wiring may retrieve it.
type Secret struct{ value string }

func (Secret) String() string   { return "[REDACTED]" }
func (Secret) GoString() string { return "Secret([REDACTED])" }

func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (Secret) MarshalYAML() (interface{}, error) {
	return "[REDACTED]", nil
}

// ManagedEntryDNSConfig contains non-secret zone settings from YAML or env.
// Token is populated only by the environment, never by YAML or serialized config.
type ManagedEntryDNSConfig struct {
	ZoneID        string `yaml:"zone_id" json:"zone_id"`
	BaseDomain    string `yaml:"base_domain" json:"base_domain"`
	Token         Secret `yaml:"-" json:"-"`
	active        bool
	validationErr error
}

func (c ManagedEntryDNSConfig) Active() bool { return c.active }

// ValidationError is nil for absent or complete settings; an invalid partial
// configuration is inactive but never prevents the API from starting.
func (c ManagedEntryDNSConfig) ValidationError() error { return c.validationErr }

// CloudflareToken is the only plaintext escape hatch, for main/client wiring.
func (c ManagedEntryDNSConfig) CloudflareToken() string { return c.Token.value }

func (c ManagedEntryDNSConfig) String() string {
	return fmt.Sprintf("ManagedEntryDNSConfig{ZoneID:%q BaseDomain:%q Token:%s Active:%t Error:%v}", c.ZoneID, c.BaseDomain, c.Token, c.active, c.validationErr)
}

func (c ManagedEntryDNSConfig) GoString() string { return c.String() }

func (c *ManagedEntryDNSConfig) resolve() {
	if c.Token.value == "" && c.ZoneID == "" && c.BaseDomain == "" {
		return
	}
	if c.Token.value == "" || c.ZoneID == "" || c.BaseDomain == "" {
		c.validationErr = ErrManagedEntryDNSIncomplete
		return
	}
	if strings.TrimSpace(c.Token.value) == "" {
		c.validationErr = ErrManagedEntryDNSToken
		return
	}
	if len(c.ZoneID) != 32 {
		c.validationErr = ErrManagedEntryDNSZoneID
		return
	}
	if _, err := hex.DecodeString(c.ZoneID); err != nil {
		c.validationErr = ErrManagedEntryDNSZoneID
		return
	}
	base, err := normalizeEntryDNSBaseDomain(c.BaseDomain)
	if err != nil {
		c.validationErr = err
		return
	}
	c.BaseDomain = base
	c.active = true
}

// normalizeEntryDNSBaseDomain mirrors Reality's ASCII hostname rules without
// importing its package. Reserve the 32-hex label plus dot for generated names.
func normalizeEntryDNSBaseDomain(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSuffix(raw, "."))
	if len(name) == 0 || len(name) > 253-33 || net.ParseIP(name) != nil {
		return "", ErrManagedEntryDNSBaseDomain
	}
	labels := strings.Split(name, ".")
	if len(labels) <= 4 && numericEntryDNSAddress(labels) {
		return "", ErrManagedEntryDNSBaseDomain
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrManagedEntryDNSBaseDomain
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return "", ErrManagedEntryDNSBaseDomain
			}
		}
	}
	return name, nil
}

func numericEntryDNSAddress(labels []string) bool {
	for _, label := range labels {
		if label == "" {
			return false
		}
		_, err := strconv.ParseUint(label, 0, 64)
		if err != nil {
			_, err = strconv.ParseUint(label, 10, 64)
		}
		if err != nil {
			return false
		}
	}
	return true
}
