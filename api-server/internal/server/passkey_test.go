package server

import "testing"

func TestPasskeyWebAuthnRequiresSecureOrigin(t *testing.T) {
	for _, tt := range []struct {
		url  string
		want bool
	}{
		{"https://portal.example.com", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:8080", false},
		{"http://portal.example.com", false},
		{"ftp://portal.example.com", false},
		{"https://user@portal.example.com", false},
		{"https://portal.example.com/path", true},
		{"", false},
	} {
		t.Run(tt.url, func(t *testing.T) {
			if got := passkeyWebAuthn(tt.url) != nil; got != tt.want {
				t.Fatalf("passkeyWebAuthn(%q) enabled = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
