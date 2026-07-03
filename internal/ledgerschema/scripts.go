package ledgerschema

import "fmt"

// Numscript builders for the alert lifecycle. Each returns one `send` block
// (multiple blocks per script are supported — the caller concatenates them into
// a single atomic transaction). Accounts are inlined as literals (`@addr`); the
// account-type patterns + STRICT enforcement are the backstop that rejects any
// address outside the declared chart. See RFC §4.1.2.

// NumscriptMintMarker mints the single ALERT marker from an overdraft issuance
// pool into a state account — the "open" primitive. The pool goes further
// negative; its -balance is the free live-alert gauge for the rule/period. The
// mint is deliberately unguarded (overdraft always succeeds); double-open is
// prevented by the batch idempotency key + serialized per-rule evaluation.
func NumscriptMintMarker(issuedPool, stateAccount string) string {
	return fmt.Sprintf(`send [%s 1] (
	source = @%s allowing unbounded overdraft
	destination = @%s
)`, AssetAlert, issuedPool, stateAccount)
}

// NumscriptMoveMarker moves the ALERT marker between two state accounts. The
// bare (non-overdraft) source makes this a compare-and-swap: the batch fails
// atomically if the marker is not currently at `from` — a free illegal-
// transition guard. The drained `from` account purges (EPHEMERAL). Shared by
// reopen/resurface (OpenOrUpdateAlert) and ack/resolve/accept (step 3c-3).
func NumscriptMoveMarker(from, to string) string {
	return fmt.Sprintf(`send [%s 1] (
	source = @%s
	destination = @%s
)`, AssetAlert, from, to)
}

// NumscriptMintOcc increments an alert item's occurrence counter by minting one
// OCC unit from the occurrence overdraft pool into the item account. The item's
// OCC balance is the occurrence count.
func NumscriptMintOcc(occPool, item string) string {
	return fmt.Sprintf(`send [%s 1] (
	source = @%s allowing unbounded overdraft
	destination = @%s
)`, AssetOcc, occPool, item)
}
