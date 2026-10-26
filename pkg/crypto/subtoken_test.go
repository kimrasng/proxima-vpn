package crypto

import (
	"regexp"
	"testing"
)

func TestNewSubToken(t *testing.T) {
	pattern := regexp.MustCompile(`^[A-Z0-9]{48}$`)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		token := NewSubToken()
		if !pattern.MatchString(token) {
			t.Fatalf("token %q is not 48 uppercase letters or digits", token)
		}
		if seen[token] {
			t.Fatalf("token %q repeated", token)
		}
		seen[token] = true
	}
}
