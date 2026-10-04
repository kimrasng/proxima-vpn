package services

import (
	"testing"
	"time"
)

func TestManagedEntryBackoffBoundsAndRetryAfter(t *testing.T) {
	// Given retry counts at the floor, cap and saturation boundary.
	for _, tc := range []struct {
		name    string
		count   int32
		minimum time.Duration
		unit    float64
		want    time.Duration
	}{
		{"first floor", 0, 0, 0, time.Second},
		{"first ceiling", 0, 0, 1, 2 * time.Second},
		{"exponential", 3, 0, 0, 8 * time.Second},
		{"capped", 40, 0, 1, 5 * time.Minute},
		{"retry after above cap", 40, 7 * time.Minute, 0, 7 * time.Minute},
		{"retry after below cap", 0, 1500 * time.Millisecond, 0, 1500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// When calculating a deterministic retry delay.
			got := ManagedEntryDNSBackoff(tc.count, tc.minimum, tc.unit)
			// Then the delay respects both bounded jitter and the server minimum.
			if got != tc.want {
				t.Fatalf("delay=%s want %s", got, tc.want)
			}
		})
	}
}
