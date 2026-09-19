package handlers

import "testing"

func TestChargedBytes(t *testing.T) {
	const gib = int64(1) << 30

	cases := []struct {
		name       string
		total      int64
		multiplier float64
		want       int64
	}{
		{"face value passes through", gib, 1.0, gib},
		{"double charges twice", gib, 2.0, 2 * gib},
		{"fractional multiplier rounds", 1000, 2.5, 2500},
		{"rounds half away from zero", 3, 1.5, 5},
		{"discount below face value is allowed", 1000, 0.5, 500},
		{"zero traffic stays zero", 0, 10.0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chargedBytes(tc.total, tc.multiplier); got != tc.want {
				t.Errorf("chargedBytes(%d, %v) = %d, want %d", tc.total, tc.multiplier, got, tc.want)
			}
		})
	}
}

// A premium node must never charge less than the bytes it moved, or the
// multiplier becomes a way to get traffic for free.
func TestChargedBytesNeverUnderchargesAbove1(t *testing.T) {
	for _, multiplier := range []float64{1.01, 1.5, 2, 3.33, 100} {
		for _, total := range []int64{1, 7, 999, 1 << 20} {
			if got := chargedBytes(total, multiplier); got < total {
				t.Errorf("chargedBytes(%d, %v) = %d, undercharged below %d", total, multiplier, got, total)
			}
		}
	}
}
