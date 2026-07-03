// Package ledgerschema is the single source of truth for reconciliation's
// control-ledger chart of accounts on Ledger v3: account-type patterns and
// persistence, the typed metadata schema, prepared queries, assets, and the
// address builders. The provisioner (bootstrap) applies it; the LedgerStore
// uses the address builders. See docs/drafts/rfc-ledger-native-storage.md §4.1.2/§4.1.3.
package ledgerschema

import (
	"crypto/sha256"
	"encoding/hex"
)

// DefaultControlLedger is the default name of the control-ledger that holds
// reconciliation's own state. Config-overridable (one per stack for the POC;
// per-tenant is a future option).
const DefaultControlLedger = "reconciliation"

// Assets minted in the control-ledger (precision 0 — they are markers/counters,
// not money).
const (
	AssetAlert = "ALERT" // lifecycle marker: exactly one unit per live alert
	AssetOcc   = "OCC"   // per-alert occurrence counter (balance on the item account)
)

// Lifecycle states — the {state} segment of a marker account. The marker sits in
// exactly one at a time; the drained ones are purged (EPHEMERAL).
const (
	StateOpen     = "open"
	StateAck      = "ack"
	StateResolved = "resolved"
	StateAccepted = "accepted"
)

// FingerprintHash maps a raw alert fingerprint (which may contain ':' and '|',
// illegal in an address segment) to a stable 16-hex-char segment. The raw
// fingerprint is stored as metadata for readability.
func FingerprintHash(fingerprint string) string {
	sum := sha256.Sum256([]byte(fingerprint))

	return hex.EncodeToString(sum[:8])
}

// --- Address builders (status-left so trailing segments aggregate by prefix) ---

// RuleAccount holds a rule's definition metadata.
func RuleAccount(ruleID string) string {
	return "rule:" + ruleID
}

// AlertItemAccount is the canonical alert account: descriptive metadata + the
// OCC balance + the mirrored status. It never holds the ALERT marker.
func AlertItemAccount(ruleID, fpHash, period string) string {
	return "alert:item:rule:" + ruleID + ":fp:" + fpHash + ":per:" + period
}

// AlertStateAccount holds the single ALERT marker for a given lifecycle state
// (EPHEMERAL). status-left: everything after :st:{state}: aggregates by prefix.
func AlertStateAccount(state, ruleID, fpHash, period string) string {
	return "alert:st:" + state + ":rule:" + ruleID + ":fp:" + fpHash + ":per:" + period
}

// IssuedPoolAccount is the overdraft source for ALERT markers of a rule/period.
// -balance(this, ALERT) = number of live alerts for that rule/period.
func IssuedPoolAccount(ruleID, period string) string {
	return "alert:issued:rule:" + ruleID + ":per:" + period
}

// OccPoolAccount is the overdraft source for OCC counter units of a rule/period,
// so every posting source is a declared account under STRICT enforcement.
func OccPoolAccount(ruleID, period string) string {
	return "alert:occ:rule:" + ruleID + ":per:" + period
}

// --- Aggregation prefixes (for AGGREGATE_VOLUMES) ---

// OpenPrefix aggregates ALERT markers across all open alerts (all rules).
func OpenPrefix() string { return "alert:st:" + StateOpen + ":" }

// OpenByRulePrefix aggregates open ALERT markers for one rule.
func OpenByRulePrefix(ruleID string) string {
	return "alert:st:" + StateOpen + ":rule:" + ruleID + ":"
}

// IssuedByRulePrefix aggregates the issuance pools of one rule (all periods).
func IssuedByRulePrefix(ruleID string) string {
	return "alert:issued:rule:" + ruleID + ":"
}
