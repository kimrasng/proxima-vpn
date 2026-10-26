package services

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestValidCodeChallenge(t *testing.T) {
	digest := sha256.Sum256([]byte("a-verifier-with-enough-entropy-for-pkce-0123456789"))
	real := base64.RawURLEncoding.EncodeToString(digest[:])

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"real S256 challenge", real, true},
		{"placeholder from docs", "BASE64URL_SHA256_OF_VERIFIER", false},
		{"empty", "", false},
		{"padded standard base64", base64.StdEncoding.EncodeToString(digest[:]), false},
		{"43 chars with plus", real[:42] + "+", false},
		{"43 chars with slash", real[:42] + "/", false},
		{"43 chars with space", real[:42] + " ", false},
		{"44 chars", real + "A", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validCodeChallenge(tc.in); got != tc.want {
				t.Fatalf("validCodeChallenge(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
