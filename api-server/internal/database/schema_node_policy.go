package database

// The agent reports the fingerprint of the forwarding policy it has installed
// (nodeprov.PolicyFingerprint) and its last reconciliation error in every
// heartbeat. NULL fingerprint means the agent predates reporting, which the
// panel shows as unknown rather than as a failure.
var nodePolicySchema = []string{
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS policy_fingerprint TEXT`,
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS policy_error TEXT`,
	`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS policy_reported_at TIMESTAMPTZ`,
}
