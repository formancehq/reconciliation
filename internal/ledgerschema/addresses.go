// Package ledgerschema is the single source of truth for reconciliation's
// control-ledger chart of accounts on Ledger v3: account-type patterns and
// persistence, the typed metadata schema, prepared queries, assets, and the
// address builders. The provisioner (bootstrap) applies it; the LedgerStore
// uses the address builders. See docs/drafts/rfc-ledger-native-storage.md §4.1.2/§4.1.3.
package ledgerschema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// DefaultControlLedger is the default name of the control-ledger that holds
// reconciliation's own state. Config-overridable (one per stack for the POC;
// per-tenant is a future option).
const DefaultControlLedger = "reconciliation"

// Assets minted in the control-ledger (precision 0 — they are markers/counters,
// not money).
const (
	AssetAlert   = "ALERT"   // lifecycle marker: exactly one unit per live alert
	AssetOcc     = "OCC"     // per-alert occurrence counter (balance on the item account)
	AssetCapture = "CAPTURE" // one unit minted per recorded evaluation (capture counter)
)

// Lifecycle states — the {state} segment of a marker account. The marker sits in
// exactly one at a time; the drained ones are purged (EPHEMERAL).
const (
	StateOpen = "open"
	StateAck  = "ack"
	// StateResolved is NOT a marker location — on close the marker is burned back
	// to the pool and its state account purges (EPHEMERAL). It survives only as
	// the status↔segment mapping's terminal value (and covers "accepted", a
	// resolution kind). Live markers sit only at StateOpen / StateAck.
	StateResolved = "resolved"
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

// ParseRuleAccount extracts the rule UUID from a `rule:{id}` address — the
// inverse of RuleAccount, kept here so the format lives in one place.
func ParseRuleAccount(address string) (uuid.UUID, error) {
	raw, ok := strings.CutPrefix(address, "rule:")
	if !ok {
		return uuid.Nil, fmt.Errorf("not a rule account: %q", address)
	}

	return uuid.Parse(raw)
}

// AlertItemAccount is the canonical alert account: descriptive metadata + the
// OCC balance + the mirrored status. It never holds the ALERT marker.
// Segment order is rule → per → fp so (rule, period) is a queryable prefix.
func AlertItemAccount(ruleID, period, fpHash string) string {
	return "alert:item:rule:" + ruleID + ":per:" + period + ":fp:" + fpHash
}

// AlertStateAccount holds the single ALERT marker for a given lifecycle state
// (EPHEMERAL). status-left so `alert:st:open:` aggregates all open markers;
// rule → per → fp so open markers of a (rule, period) are a prefix (the
// auto-resolve sweep, ListActiveAlertFingerprints).
func AlertStateAccount(state, ruleID, period, fpHash string) string {
	return "alert:st:" + state + ":rule:" + ruleID + ":per:" + period + ":fp:" + fpHash
}

// PoolAccount is the per-rule overdraft source: it mints both the ALERT markers
// and the OCC counter units (distinct assets, independent balances) for every
// period of the rule. Keyed by rule only (not rule+period) so the source
// footprint is O(#rules), not O(#rules×#periods) — the period scoping added no
// value the marker/item aggregations don't already give. Keeping every posting
// source a declared account satisfies STRICT enforcement. Two per-rule gauges
// live on it: -balance(this, ALERT) = live alerts for the rule (all periods);
// -balance(this, OCC) = total occurrences for the rule.
func PoolAccount(ruleID string) string {
	return "alert:pool:rule:" + ruleID
}

// CaptureAccount is the (rule, period) capture bucket: every evaluation records an
// immutable capture transaction here (ADR-003), so the account's transaction log is
// the period's ordered series of captures and -balance(this, CAPTURE) counts them.
// The observed state lives in each capture transaction's metadata, not on the account.
func CaptureAccount(ruleID, period string) string {
	return "capture:rule:" + ruleID + ":per:" + period
}

// CapturePool is the per-rule overdraft source that mints the CAPTURE marker for
// every capture of the rule (keyed by rule → O(#rules) source accounts, mirroring
// the alert pool). A declared source keeps STRICT enforcement satisfied.
func CapturePool(ruleID string) string {
	return "capture:pool:rule:" + ruleID
}

// CaptureRulePrefix matches all capture buckets of a rule across every period
// (`capture:rule:{ruleID}:per:*`). Used to list a rule's full capture history as
// transactions. It excludes the rule's capture pool (`capture:pool:rule:*`), so a
// prefix match returns only the per-period buckets, not the mint source.
func CaptureRulePrefix(ruleID string) string {
	return "capture:rule:" + ruleID + ":per:"
}

// --- Aggregation prefixes (for AGGREGATE_VOLUMES) ---

// OpenPrefix aggregates ALERT markers across all open alerts (all rules).
func OpenPrefix() string { return "alert:st:" + StateOpen + ":" }

// OpenByRulePrefix aggregates open ALERT markers for one rule (all periods).
func OpenByRulePrefix(ruleID string) string {
	return "alert:st:" + StateOpen + ":rule:" + ruleID + ":"
}

// OpenByRulePeriodPrefix matches the open ALERT markers of a (rule, period). The
// trailing segment of each match is `fp:{fpHash}` — the auto-resolve sweep reads
// them to list active fingerprints (ListActiveAlertFingerprints).
func OpenByRulePeriodPrefix(ruleID, period string) string {
	return "alert:st:" + StateOpen + ":rule:" + ruleID + ":per:" + period + ":"
}

// RulePrefix matches all rule-definition accounts.
func RulePrefix() string { return "rule:" }

// ItemPrefix matches all alert item accounts (used to scope metadata queries,
// e.g. resolving an alert by its indexed `id`).
func ItemPrefix() string { return "alert:item:" }

// ItemByRulePeriodPrefix matches all alert items of a (rule, period) — the scope
// for the auto-resolve sweep's active-fingerprint scan.
func ItemByRulePeriodPrefix(ruleID, period string) string {
	return "alert:item:rule:" + ruleID + ":per:" + period + ":"
}
