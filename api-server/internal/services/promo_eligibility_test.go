package services

import "testing"

func TestPromoRulesAllows(t *testing.T) {
	rules := PromoRules{
		Countries: []string{"KR"}, Browsers: []string{"chrome"},
		IPCIDRs: []string{"192.0.2.0/24"}, EmailDomains: []string{"example.test"},
	}
	for _, tc := range []struct {
		name    string
		visitor PromoVisitor
		want    bool
	}{
		{"match", PromoVisitor{"KR", "chrome", "192.0.2.1", "a@example.test"}, true},
		{"anonymous", PromoVisitor{"KR", "chrome", "192.0.2.1", ""}, true},
		{"unknown country", PromoVisitor{"", "chrome", "192.0.2.1", "a@example.test"}, false},
		{"country", PromoVisitor{"US", "chrome", "192.0.2.1", "a@example.test"}, false},
		{"browser", PromoVisitor{"KR", "safari", "192.0.2.1", "a@example.test"}, false},
		{"ip", PromoVisitor{"KR", "chrome", "198.51.100.1", "a@example.test"}, false},
		{"email", PromoVisitor{"KR", "chrome", "192.0.2.1", "a@other.test"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.Allows(tc.visitor); got != tc.want {
				t.Fatalf("Allows() = %v, want %v", got, tc.want)
			}
		})
	}
}
