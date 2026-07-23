// Package engine implements Ledger Clarity's internal rule kernel — a CEL
// evaluator bound to a typed object model with Source as a first-class abstraction.
//
// This package is NOT a public V1 API. Templates compile to expressions
// evaluated by this kernel; only post-GA design-partner mode exposes raw CEL.
// See ADR-001 for the rationale and ADR-002 for the consistency model.
package engine

import (
	"math/big"
	"time"
)

// Account is the typed CEL view of a ledger account at a specific PIT.
// Fields are intentionally narrow — only what V1 templates and post-GA
// expressions need. Extending this surface is a breaking change for power-mode
// customers; review with the same rigor as a REST contract change.
type Account struct {
	Address      string
	Ledger       string
	Metadata     map[string]string
	Balance      *big.Int           // single-asset shortcut: meaningful only when caller scoped to one asset
	Balances     map[string]*big.Int
	LastActivity time.Time
}

// Balance pairs an asset code with its amount. The amount uses *big.Int because
// the underlying ledger holds amounts as arbitrary-precision integers and we
// must not lose precision when shuttling values through CEL.
type Balance struct {
	Asset  string
	Amount *big.Int
}

// Posting is the typed CEL view of an individual ledger posting. Reserved for
// V1.1 templates (posting_rate, account_inactivity). V1 GA does not expose it.
type Posting struct {
	TxID        string
	Source      string
	Destination string
	Asset       string
	Amount      *big.Int
	At          time.Time
}
