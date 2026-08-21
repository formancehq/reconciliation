package audit

import (
	"time"

	"github.com/google/uuid"
	"github.com/zeebo/blake3"

	"github.com/formancehq/reconciliation/internal/models"
)

const (
	// stateContext domain-separates the derived-state hash.
	stateContext = "formance:reconciliation:period-state:v1"
)

// StateHasher accumulates a period's derived alert state into one digest.
//
// The encoding lives here rather than in SQL for the same reason the chain hash
// does: one implementation, in one language. The caller is responsible only for
// feeding rows in a deterministic order — the storage layer does that with an
// explicit ORDER BY, never relying on an aggregate's input order, which is the
// determinism trap in the Ledger V2 block hasher.
type StateHasher struct {
	h *blake3.Hasher
	n int64
}

// NewStateHasher starts an accumulator.
func NewStateHasher() *StateHasher {
	h := blake3.New()
	w := &payloadWriter{}
	w.stringField(stateContext)
	_, _ = h.Write(w.bytes())
	return &StateHasher{h: h}
}

// AlertState is the subset of an alert the seal commits to: its identity, where
// it ended up, and how it got there. Evidence is not included — it is already
// bound by the evaluation entries inside the sealed range, and duplicating it
// here would double the seal's cost for no additional guarantee.
type AlertState struct {
	ID              uuid.UUID
	RuleID          uuid.UUID
	Fingerprint     string
	Status          models.AlertStatus
	Severity        models.Severity
	OccurrenceCount int64
	ResolutionKind  string
}

// AddAlert folds one alert into the digest. Call in a deterministic order.
func (s *StateHasher) AddAlert(a AlertState) {
	w := &payloadWriter{}
	id := a.ID
	w.bytesField(id[:])
	rid := a.RuleID
	w.bytesField(rid[:])
	w.stringField(a.Fingerprint)
	w.stringField(string(a.Status))
	w.stringField(string(a.Severity))
	w.uint64(uint64(a.OccurrenceCount))
	w.stringField(a.ResolutionKind)
	_, _ = s.h.Write(w.bytes())
	s.n++
}

// Count returns how many alerts have been folded in.
func (s *StateHasher) Count() int64 { return s.n }

// Sum closes the accumulator. The count is folded in last so that a truncated
// scan cannot produce the same digest as a complete one.
func (s *StateHasher) Sum() []byte {
	w := &payloadWriter{}
	w.uint64(uint64(s.n))
	_, _ = s.h.Write(w.bytes())

	out := make([]byte, digestLen)
	_, _ = s.h.Digest().Read(out)
	return out
}

// closureContext domain-separates a closure's sealing hash from a period seal's.
// Deliberately a different constant: the two attest different statements, and a
// value that verified under one scheme must not verify under the other.
const closureContext = "formance:reconciliation:closure-seal:v1"

// ClosureInput is everything a closure's seal commits to.
//
// The per-period breakdown is bound through StateHash rather than listed here,
// so this stays a fixed, short field list an auditor can reimplement — the same
// reason ComputeSealingHash is unkeyed.
type ClosureInput struct {
	ClosureID     int64
	FirstSequence int64
	LastSequence  int64
	EntryCount    int64
	LastAuditHash []byte
	// StateHash covers the per-period breakdown: every period id in the closure
	// with its counts and its own state hash. Without it the closure would attest
	// a range and leave the business figures unsigned — the exact gap that let an
	// edited alert count read "0 still open" while verification answered intact.
	StateHash []byte
	ClosedBy  models.Subject
	ClosedAt  time.Time
}

// ClosureInputFor is the one mapping from a stored closure to what its seal
// covers, for the same reason SealInputFor exists: signing and every
// verification path must agree by construction rather than by review.
//
// closure must be non-nil and closed. An open closure has no LastSequence, and
// hashing one would attest a boundary that has not happened.
func ClosureInputFor(closure *models.Closure) ClosureInput {
	var last int64
	if closure.LastSequence != nil {
		last = *closure.LastSequence
	}
	var closedAt time.Time
	if closure.ClosedAt != nil {
		closedAt = *closure.ClosedAt
	}
	return ClosureInput{
		ClosureID:     closure.ID,
		FirstSequence: closure.FirstSequence,
		LastSequence:  last,
		EntryCount:    closure.EntryCount,
		LastAuditHash: closure.LastAuditHash,
		StateHash:     closure.StateHash,
		ClosedBy:      closure.ClosedBy,
		ClosedAt:      closedAt,
	}
}

// WithHead substitutes the chain head the journal actually presents at the
// closure's boundary, so a chain walk catches a closure moved onto a different
// journal rather than merely one that is internally consistent.
func (in ClosureInput) WithHead(headHash []byte) ClosureInput {
	in.LastAuditHash = headHash
	return in
}

// ComputeClosureHash returns the single value that stands for a whole closure.
//
// Unkeyed and reproducible from the closure's published fields, for the same
// reason as ComputeSealingHash: an auditor holding those fields and the public
// key must be able to check our claim without our cooperation.
func ComputeClosureHash(in ClosureInput) []byte {
	w := &payloadWriter{}
	w.stringField(closureContext)
	w.uint64(uint64(in.ClosureID))
	w.uint64(uint64(in.FirstSequence))
	w.uint64(uint64(in.LastSequence))
	w.uint64(uint64(in.EntryCount))
	w.bytesField(in.LastAuditHash)
	w.bytesField(in.StateHash)
	w.bytesField(EncodeSubject(in.ClosedBy))
	// Microseconds, because that is all timestamptz stores. Hashing the raw
	// reading would make every closure verify as tampered on Linux and pass on
	// macOS.
	w.uint64(uint64(in.ClosedAt.UTC().Truncate(time.Microsecond).UnixMicro()))

	sum := blake3.Sum256(w.bytes())
	return sum[:]
}

// ClosureStateHasher folds a closure's per-period breakdown into one digest, in
// an order it states explicitly rather than one the query happens to return.
type ClosureStateHasher struct {
	w *payloadWriter
}

func NewClosureStateHasher() *ClosureStateHasher {
	w := &payloadWriter{}
	w.stringField(stateContext)
	return &ClosureStateHasher{w: w}
}

// Add folds one period's figures in. Callers must add periods in a deterministic
// order — sorted by period id — because a digest over a set is only meaningful
// if the set has an agreed sequence.
func (h *ClosureStateHasher) Add(p models.ClosurePeriod) {
	h.w.stringField(p.PeriodID)
	h.w.uint64(uint64(p.EntryCount))
	h.w.uint64(uint64(p.AlertCount))
	h.w.uint64(uint64(p.UnresolvedCount))
	h.w.stringField(p.StateHash)
	// Both flags, not just Ended. Frozen is what an operator reads to know whether
	// a period still accepts writes, and a published field outside the signature
	// is a field anyone can restate — the same gap that once let an edited alert
	// count read "0 still open" while verification answered intact.
	if p.Ended {
		h.w.byteTag(0x01)
	} else {
		h.w.byteTag(0x00)
	}
	if p.Frozen {
		h.w.byteTag(0x01)
	} else {
		h.w.byteTag(0x00)
	}
}

func (h *ClosureStateHasher) Sum() []byte {
	sum := blake3.Sum256(h.w.bytes())
	return sum[:]
}
