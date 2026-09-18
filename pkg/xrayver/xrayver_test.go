package xrayver

import "testing"

func TestCompareOrdersVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v26.3.27", "v25.1.1", 1},
		{"v25.1.1", "v26.3.27", -1},
		{"v26.3.27", "v26.3.27", 0},
		{"26.3.27", "v26.3.27", 0},
		{"v26.3.27", "v26.3.9", 1},
		{"v26.10.1", "v26.9.30", 1},
		{"v26.3", "v26.3.0", 0},
		{"v26.3.1", "v26.3", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// Numeric ordering, not lexicographic: "26.3.9" vs "26.3.27" is the case a
// string comparison gets backwards.
func TestComparePartsAreNumericNotTextual(t *testing.T) {
	if Compare("v26.3.27", "v26.3.9") != 1 {
		t.Error("26.3.27 should sort above 26.3.9")
	}
	if Compare("v9.1.1", "v26.1.1") != -1 {
		t.Error("9.1.1 should sort below 26.1.1")
	}
}

func TestMinimumItselfPasses(t *testing.T) {
	if !AtLeastMinimum(Minimum) {
		t.Fatalf("the minimum version %q does not satisfy itself", Minimum)
	}
}

func TestOlderVersionsAreRejected(t *testing.T) {
	for _, v := range []string{"v24.12.31", "v1.8.4", "v25.1.0"} {
		if AtLeastMinimum(v) {
			t.Errorf("%q should not satisfy the %s minimum", v, Minimum)
		}
	}
}

func TestNewerVersionsAreAccepted(t *testing.T) {
	for _, v := range []string{"v25.1.2", "v26.3.27", "v27.0.0"} {
		if !AtLeastMinimum(v) {
			t.Errorf("%q should satisfy the %s minimum", v, Minimum)
		}
	}
}

// A node that has not reported a version must not be treated as too old -
// silence would otherwise lock out nodes for saying nothing.
func TestUnknownVersionIsAccepted(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if !AtLeastMinimum(v) {
			t.Errorf("empty version %q was rejected", v)
		}
	}
}

// A build suffix must not change ordering, and must not make the whole version
// unparseable.
func TestBuildSuffixIsIgnored(t *testing.T) {
	if Compare("v26.3.27-1", "v26.3.27") != 0 {
		t.Error("a build suffix changed the ordering")
	}
	if !AtLeastMinimum("v26.3.27-custom") {
		t.Error("a suffixed version was rejected")
	}
}

// Garbage must not read as "newer" - that would silently disable the check -
// nor should it panic.
func TestUnparseableVersionDoesNotPassAsNewer(t *testing.T) {
	if AtLeastMinimum("not-a-version") {
		t.Error("an unparseable version satisfied the minimum")
	}
}

func TestExplainNamesBothVersions(t *testing.T) {
	msg := Explain("v1.8.4")
	for _, want := range []string{"v1.8.4", Minimum} {
		if !contains(msg, want) {
			t.Errorf("Explain output %q does not mention %q", msg, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
