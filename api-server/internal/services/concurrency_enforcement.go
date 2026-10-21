package services

import (
	"os"
	"strings"
)

// ConcurrencyEnforcementEnvVar is the kill switch for UUID concurrency
// enforcement. Enforcement is ON unless this is explicitly set to a false-like
// value. It is read when the scheduler is built and per request by the admin
// endpoint, so the panel always reports the state the scheduler runs with.
const ConcurrencyEnforcementEnvVar = "ENFORCE_CONCURRENCY"

// ConcurrencyEnforcementEnabled reports whether over-cap online UUID slots are
// evicted. It is on by default; set the variable to 0, false, off or no to
// fall back to observation only. Any other value, including a typo, leaves
// enforcement on, so a mistyped override can never silently disable the cap.
// Eviction itself stays guarded: it never happens on incomplete or ambiguous
// observations or while any Exit is stale.
func ConcurrencyEnforcementEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ConcurrencyEnforcementEnvVar))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}
