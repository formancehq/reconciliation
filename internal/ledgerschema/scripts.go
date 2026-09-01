package ledgerschema

import "fmt"

// Numscript library for the alert lifecycle. The transition programs are stored
// in the ledger's numscript library (SaveNumscript, validated at save time) and
// referenced by name+version at transaction time (ScriptReference), instead of
// inlining the source in every request. Accounts are passed as vars — addresses
// are dynamic (rule/period/fp). STRICT enforcement will reject any address
// outside the declared chart once enabled; the current rollout uses AUDIT. See RFC §4.1.2 and
// docs/technical/architecture/subsystems/scripting/numscript-library.md.

// Numscript names and the pinned version. Library semver is immutable: bump the
// version when a program's content changes, and update the reference in lockstep.
const (
	NumscriptVersion = "2.0.0"

	NumscriptAlertOpen   = "alert_open"   // mint marker → st:open + mint OCC → item (new alert)
	NumscriptAlertBump   = "alert_bump"   // mint OCC → item (repeat, marker stays put)
	NumscriptAlertReopen = "alert_reopen" // guarded move st:{from}→st:open + mint OCC (reopen/resurface)
	NumscriptAlertMove   = "alert_move"   // guarded move st:{from}→st:{to}, no OCC (ack/resolve/accept/auto-resolve)
	NumscriptCapture     = "capture"      // mint 1 CAPTURE → capture bucket (records one evaluation)
	NumscriptActivity    = "activity"     // append one event to a rule's activity stream
)

// Numscript var names — the account addresses passed per call.
const (
	VarPool   = "pool"    // the (rule, period) overdraft source pool
	VarItem   = "item"    // the canonical alert item account
	VarStOpen = "st_open" // the st:open marker account
	VarStFrom = "st_from" // the marker's current state account (guarded move source)
	VarStTo   = "st_to"   // the marker's target state account (guarded move destination)

	VarCapturePool  = "capture_pool" // the per-rule capture overdraft source
	VarCapture      = "capture"      // the (rule, period) capture bucket account
	VarActivityPool = "activity_pool"
	VarActivity     = "activity"
)

// NumscriptDef is one library program to register at provisioning.
type NumscriptDef struct {
	Name    string
	Content string
	Version string
}

// Numscripts returns the alert-lifecycle programs to register on the
// control-ledger (idempotent — the client swallows AlreadyExists on re-save of
// the same immutable version).
func Numscripts() []NumscriptDef {
	return []NumscriptDef{
		{NumscriptAlertOpen, alertOpenContent(), NumscriptVersion},
		{NumscriptAlertBump, alertBumpContent(), NumscriptVersion},
		{NumscriptAlertReopen, alertReopenContent(), NumscriptVersion},
		{NumscriptAlertMove, alertMoveContent(), NumscriptVersion},
		{NumscriptCapture, captureContent(), NumscriptVersion},
		{NumscriptActivity, activityContent(), NumscriptVersion},
	}
}

// captureContent mints a single CAPTURE marker into the (rule, period) capture
// bucket (overdraft source, unguarded — one capture per evaluation, deduped by the
// batch idempotency key). The minimal posting carries the transaction; the observed
// snapshot rides the transaction metadata (ADR-003).
func captureContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
	account $%[3]s
	account $%[4]s
}
send [%[5]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[2]s
)
send [%[6]s 1] (
	source = $%[3]s allowing unbounded overdraft
	destination = $%[4]s
)`, VarCapturePool, VarCapture, VarActivityPool, VarActivity, AssetCapture, AssetActivity)
}

func activityContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
}
send [%[3]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[2]s
)`, VarActivityPool, VarActivity, AssetActivity)
}

// alertOpenContent mints the single ALERT marker into st:open (overdraft, the
// mint is unguarded — double-open is prevented by the batch idempotency key) and
// the first OCC unit, both from the (rule, period) pool.
func alertOpenContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
	account $%[3]s
	account $%[4]s
	account $%[5]s
}
send [%[6]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[2]s
)
send [%[7]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[3]s
)
send [%[8]s 1] (
	source = $%[4]s allowing unbounded overdraft
	destination = $%[5]s
)`, VarPool, VarStOpen, VarItem, VarActivityPool, VarActivity, AssetAlert, AssetOcc, AssetActivity)
}

// alertBumpContent increments the item's OCC counter by one (a repeat failure —
// the marker already sits at st:open, so no move).
func alertBumpContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
	account $%[3]s
	account $%[4]s
}
send [%[5]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[2]s
)
send [%[6]s 1] (
	source = $%[3]s allowing unbounded overdraft
	destination = $%[4]s
)`, VarPool, VarItem, VarActivityPool, VarActivity, AssetOcc, AssetActivity)
}

// alertMoveContent moves the ALERT marker between two state accounts, no OCC
// bump — the guarded lifecycle transition for ack (open→ack), resolve/accept
// ({open,ack}→resolved) and auto-resolve. The bare source is the compare-and-swap.
func alertMoveContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
	account $%[3]s
	account $%[4]s
}
send [%[5]s 1] (
	source = $%[1]s
	destination = $%[2]s
)
send [%[6]s 1] (
	source = $%[3]s allowing unbounded overdraft
	destination = $%[4]s
)`, VarStFrom, VarStTo, VarActivityPool, VarActivity, AssetAlert, AssetActivity)
}

// alertReopenContent moves the ALERT marker st:{from}→st:open and bumps OCC. The
// bare (non-overdraft) source makes the move a compare-and-swap: the batch fails
// atomically if the marker is not at $st_from. The drained account purges
// (EPHEMERAL). Used for reopen (from st:resolved) and resurface (from st:ack).
func alertReopenContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
	account $%[3]s
	account $%[4]s
	account $%[5]s
	account $%[6]s
}
send [%[7]s 1] (
	source = $%[2]s
	destination = $%[3]s
)
send [%[8]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[4]s
)
send [%[9]s 1] (
	source = $%[5]s allowing unbounded overdraft
	destination = $%[6]s
)`, VarPool, VarStFrom, VarStOpen, VarItem, VarActivityPool, VarActivity, AssetAlert, AssetOcc, AssetActivity)
}
