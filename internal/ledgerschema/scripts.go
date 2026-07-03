package ledgerschema

import "fmt"

// Numscript library for the alert lifecycle. The transition programs are stored
// in the ledger's numscript library (SaveNumscript, validated at save time) and
// referenced by name+version at transaction time (ScriptReference), instead of
// inlining the source in every request. Accounts are passed as vars — addresses
// are dynamic (rule/period/fp) — and STRICT enforcement is the backstop that
// rejects any address outside the declared chart. See RFC §4.1.2 and
// docs/technical/architecture/subsystems/scripting/numscript-library.md.

// Numscript names and the pinned version. Library semver is immutable: bump the
// version when a program's content changes, and update the reference in lockstep.
const (
	NumscriptVersion = "1.0.0"

	NumscriptAlertOpen   = "alert_open"   // mint marker → st:open + mint OCC → item (new alert)
	NumscriptAlertBump   = "alert_bump"   // mint OCC → item (repeat, marker stays put)
	NumscriptAlertReopen = "alert_reopen" // guarded move st:{from}→st:open + mint OCC (reopen/resurface)
)

// Numscript var names — the account addresses passed per call.
const (
	VarPool   = "pool"    // the (rule, period) overdraft source pool
	VarItem   = "item"    // the canonical alert item account
	VarStOpen = "st_open" // the st:open marker account
	VarStFrom = "st_from" // the marker's current state account (guarded move source)
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
	}
}

// alertOpenContent mints the single ALERT marker into st:open (overdraft, the
// mint is unguarded — double-open is prevented by the batch idempotency key) and
// the first OCC unit, both from the (rule, period) pool.
func alertOpenContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
	account $%[3]s
}
send [%[4]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[2]s
)
send [%[5]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[3]s
)`, VarPool, VarStOpen, VarItem, AssetAlert, AssetOcc)
}

// alertBumpContent increments the item's OCC counter by one (a repeat failure —
// the marker already sits at st:open, so no move).
func alertBumpContent() string {
	return fmt.Sprintf(`vars {
	account $%[1]s
	account $%[2]s
}
send [%[3]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[2]s
)`, VarPool, VarItem, AssetOcc)
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
}
send [%[5]s 1] (
	source = $%[2]s
	destination = $%[3]s
)
send [%[6]s 1] (
	source = $%[1]s allowing unbounded overdraft
	destination = $%[4]s
)`, VarPool, VarStFrom, VarStOpen, VarItem, AssetAlert, AssetOcc)
}
