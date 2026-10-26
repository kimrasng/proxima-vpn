package payments

import (
	"strings"
	"testing"
)

func TestCouponDisplayName(t *testing.T) {
	// The expectation is computed, not hardcoded: the function's contract is
	// "at most 40 runes, empty falls back to Promotion".
	check := func(code string) {
		t.Helper()
		got := couponDisplayName(code)
		if len([]rune(got)) > 40 {
			t.Errorf("couponDisplayName(%q) = %q has %d runes, want <= 40", code, got, len([]rune(got)))
		}
		if code == "" {
			if got != "Promotion" {
				t.Errorf("couponDisplayName with empty code = %q, want %q", got, "Promotion")
			}
			return
		}
		if len([]rune(code)) <= 40 && got != code {
			t.Errorf("couponDisplayName(%q) = %q, short code must be kept as-is", code, got)
		}
		if len([]rune(code)) > 40 && got != string([]rune(code)[:40]) {
			t.Errorf("couponDisplayName(%q) = %q, want the first 40 runes", code, got)
		}
	}

	check("")
	check("SUMMER50")
	check(strings.Repeat("A", 40))
	check(strings.Repeat("A", 41))
	check("THIS-IS-A-VERY-LONG-PROMOTION-CODE-THAT-EXCEEDS-FORTY")
	check(strings.Repeat("프로모션", 20))
}
