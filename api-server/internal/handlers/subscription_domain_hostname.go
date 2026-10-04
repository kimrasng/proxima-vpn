package handlers

import (
	"errors"
	"net"
	"strings"
)

var errInvalidSubscriptionHostname = errors.New("subscription domain must be a DNS hostname without a port")

// parseSubscriptionHostname stores the lowercase ASCII DNS name used unchanged by DNS and TLS probes.
func parseSubscriptionHostname(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return "", errInvalidSubscriptionHostname
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errInvalidSubscriptionHostname
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return "", errInvalidSubscriptionHostname
			}
		}
	}
	return host, nil
}
