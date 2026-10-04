package services

import (
	"math"
	"time"
)

// ManagedEntryDNSBackoff computes equal-jitter backoff, with Retry-After as a floor.
func ManagedEntryDNSBackoff(retryCount int32, retryAfter time.Duration, jitterUnit float64) time.Duration {
	cap := 2 * time.Second
	for i := int32(0); i < retryCount && cap < 5*time.Minute; i++ {
		cap *= 2
		if cap > 5*time.Minute {
			cap = 5 * time.Minute
		}
	}
	unit := math.Max(0, math.Min(1, jitterUnit))
	delay := cap/2 + time.Duration(float64(cap/2)*unit)
	if retryAfter > delay {
		return retryAfter
	}
	return delay
}
