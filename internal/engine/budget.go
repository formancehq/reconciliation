package engine

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Limits are the per-evaluation hard ceilings the engine enforces. Defaults
// come from DefaultLimits; production callers may override per-engine.
//
// Hitting any limit yields an EvaluationError (separate engine.error
// meta-incident path), never a silent truncation.
type Limits struct {
	// MaxCELCost is the cel-go cost-limit budget. Coarse estimate of evaluation
	// work; 1M units is enough for our V1 templates against ledgers <100k
	// accounts. Tune from telemetry in the V1.1 cycle.
	MaxCELCost uint64

	// MaxAccountsScanned caps how many accounts a single evaluation can fetch
	// across all ListAccounts calls. Prevents `accounts(ledgerSet(q)).all(...)`
	// from quietly fanning out across a million-account ledger.
	MaxAccountsScanned int

	// MaxWallClock is enforced by Engine.Evaluate via a context deadline.
	MaxWallClock time.Duration
}

// DefaultLimits is the engine's out-of-the-box budget. Conservative on purpose:
// design partners and EE customers can override per-engine, but a Formance dev
// or fresh integration should never hit these unless they're doing something
// genuinely large.
var DefaultLimits = Limits{
	MaxCELCost:         1_000_000,
	MaxAccountsScanned: 50_000,
	MaxWallClock:       30 * time.Second,
}

// mergeLimits fills zero-valued fields in `override` with the corresponding
// fields from `defaults`. Lets callers pass partial Limits — a zero
// MaxCELCost or MaxAccountsScanned must not silently disable the check, since
// disabling those guards was never a documented opt-in.
func mergeLimits(override, defaults Limits) Limits {
	if override.MaxCELCost == 0 {
		override.MaxCELCost = defaults.MaxCELCost
	}
	if override.MaxAccountsScanned == 0 {
		override.MaxAccountsScanned = defaults.MaxAccountsScanned
	}
	if override.MaxWallClock == 0 {
		override.MaxWallClock = defaults.MaxWallClock
	}
	return override
}

// budgetTracker is the per-evaluation account-scan accumulator. CEL runtime
// cost is tracked independently by cel-go and persisted as cost_units.
type budgetTracker struct {
	limits          Limits
	accountsScanned atomic.Int64
}

func newBudgetTracker(limits Limits) *budgetTracker {
	return &budgetTracker{limits: limits}
}

// ChargeAccounts records `n` more accounts scanned and returns an error if the
// running total exceeds the budget. Always check the error and abort the work
// if non-nil.
func (b *budgetTracker) ChargeAccounts(n int) error {
	if n <= 0 {
		return nil
	}
	total := b.accountsScanned.Add(int64(n))
	if int(total) > b.limits.MaxAccountsScanned {
		return fmt.Errorf("evaluation budget exceeded: scanned %d accounts (limit %d)", total, b.limits.MaxAccountsScanned)
	}
	return nil
}

// AccountsScanned returns the running account total.
func (b *budgetTracker) AccountsScanned() int64 {
	return b.accountsScanned.Load()
}
