package services

import (
	"os"
	"strings"
)

// ClientAddressLimitEnvVar is the kill switch for client address cap
// enforcement. Enforcement is ON unless this is explicitly set to a false-like
// value. It is read once when the scheduler is built and per request by the
// admin endpoint, so both always report the same state.
const ClientAddressLimitEnvVar = "ENFORCE_CLIENT_ADDRESS_LIMIT"

// ClientAddressEnforcementEnabled reports whether over-cap client addresses
// have their transports closed. It is on by default; set the variable to
// 0, false, off or no to turn it off. Any other value, including a typo,
// leaves enforcement on, so a mistyped override can never silently disable the
// limit. The setting is independent of ENFORCE_CONCURRENCY and never causes a
// UUID to be revoked: only the exact transports of denied addresses are closed.
func ClientAddressEnforcementEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ClientAddressLimitEnvVar))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}
