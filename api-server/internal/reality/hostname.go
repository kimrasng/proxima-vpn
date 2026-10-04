package reality

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var ErrInvalidHostname = errors.New("invalid Reality SNI hostname")

type Hostname string

func (h Hostname) String() string { return string(h) }

// NormalizeHostname accepts ASCII DNS names (including punycode). One final
// DNS root dot is accepted but omitted from the canonical value.
func NormalizeHostname(raw string) (Hostname, error) {
	name := strings.ToLower(strings.TrimSuffix(raw, "."))
	if len(name) == 0 || len(name) > 253 || net.ParseIP(name) != nil {
		return "", fmt.Errorf("%q: %w", raw, ErrInvalidHostname)
	}
	labels := strings.Split(name, ".")
	if len(labels) <= 4 && numericAddress(labels) {
		return "", fmt.Errorf("%q: %w", raw, ErrInvalidHostname)
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%q: %w", raw, ErrInvalidHostname)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return "", fmt.Errorf("%q: %w", raw, ErrInvalidHostname)
			}
		}
	}
	return Hostname(name), nil
}

// numericAddress rejects the shortened, octal, and hexadecimal IPv4 forms
// accepted by URL hosts, beyond the canonical forms caught by net.ParseIP.
func numericAddress(labels []string) bool {
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
