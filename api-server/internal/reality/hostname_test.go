package reality

import (
	"errors"
	"strings"
	"testing"
)

func TestPHS028NormalizeHostname_whenDNSNameIsCanonical(t *testing.T) {
	// Given
	cases := map[string]string{
		"EXAMPLE.org": "example.org", "XN--BCHER-KVA.Example.": "xn--bcher-kva.example", "a": "a",
		strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61): strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61),
	}
	for input, want := range cases {
		// When
		got, err := NormalizeHostname(input)
		// Then
		if err != nil || got.String() != want {
			t.Errorf("NormalizeHostname(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

func TestPHS028NormalizeHostname_whenInputIsNotDNS(t *testing.T) {
	// Given
	cases := []string{"", " example.org", "example.org ", "a b.org", "a\tb.org", "bücher.org", "127.0.0.1", "127.1", "0177.0.0.1", "0x7f000001", "2130706433", "[::1]", "https://example.org", "user@example.org", "example.org:443", "example.org/path", "example.org?q=1", "example.org#x", "*.example.org", "a..org", ".example.org", "example.org..", "-bad.org", "bad-.org", "a_b.org", strings.Repeat("a", 64) + ".org", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62)}
	for _, input := range cases {
		// When
		_, err := NormalizeHostname(input)
		// Then
		if !errors.Is(err, ErrInvalidHostname) {
			t.Errorf("NormalizeHostname(%q) error = %v; want ErrInvalidHostname", input, err)
		}
	}
}
