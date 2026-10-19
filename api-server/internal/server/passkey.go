package server

import (
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/webauthn"
)

// passkeyWebAuthn only enables WebAuthn on HTTPS or the localhost secure-context exception.
func passkeyWebAuthn(panelURL string) *webauthn.WebAuthn {
	u, err := url.Parse(strings.TrimSpace(panelURL))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1")) {
		return nil
	}
	origins := []string{u.Scheme + "://" + u.Host}
	// The browser e2e suite deliberately serves Vite on localhost:8080 and the
	// API on localhost:2053; localhost is a secure context and RP IDs are portless.
	if u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" {
		origins = append(origins, "http://localhost:8080")
	}
	wa, err := webauthn.New(&webauthn.Config{RPID: u.Hostname(), RPDisplayName: "Proxima Portal", RPOrigins: origins})
	if err != nil {
		return nil
	}
	return wa
}
