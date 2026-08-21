package audit

import (
	"github.com/google/uuid"
	"github.com/zeebo/blake3"

	"github.com/formancehq/reconciliation/internal/models"
)

const (
	// sealContext domain-separates the sealing hash from every other digest the
	// module produces.
	sealContext = "formance:reconciliation:period-seal:v1"
	// stateContext domain-separates the derived-state hash.
	stateContext = "formance:reconciliation:period-state:v1"
)

// SealInput is everything a period seal commits to.
type SealInput struct {
	PeriodID      string
	FirstSequence int64
	LastSequence  int64
	EntryCount    int64
	// LastAuditHash is the chain head at seal time. Verification of the chain
	// resumes across a sealed boundary from this value, the way the Ledger
	// resumes across an archived chapter using its sealed last_audit_hash.
	LastAuditHash []byte
	// StateHash covers the period's derived alert state, so the seal commits to
	// the outcome and not only to the journal.
	StateHash []byte
}

// SealInputFor is the one mapping from a stored seal to the fields its sealing
// hash covers.
//
// Four paths compute a sealing hash — signing at seal time, VerifySealIntegrity,
// VerifySealSignature and the chain walk's verifySealAgainstHead — and they must
// agree exactly. When each spelled the mapping out for itself, adding a committed
// field to some of them and not the others would have left signing, integrity
// verification and chain walking contradicting one another over the same seal:
// a seal signed over the new field would fail to re-derive on a path that still
// hashed the old set, reported as tampering that never happened. Centralising it
// makes that class of drift impossible to introduce by omission.
//
// seal must be non-nil, and deliberately is not guarded: every caller reaches
// here from a stored row, and GetPeriodSeal reports an open period as ErrNotFound
// rather than a nil seal. Returning a zero SealInput for nil would be the
// dangerous reading — it would hash a seal that commits to nothing, and let an
// all-zero forgery verify. Failing loudly is the only safe behaviour a verifier
// can have.
func SealInputFor(seal *models.PeriodSeal) SealInput {
	return SealInput{
		PeriodID:      seal.PeriodID,
		FirstSequence: seal.FirstSequence,
		LastSequence:  seal.LastSequence,
		EntryCount:    seal.EntryCount,
		LastAuditHash: seal.LastAuditHash,
		StateHash:     seal.StateHash,
	}
}

// WithHead substitutes the chain head the journal actually presents at the seal's
// boundary for the one the seal records.
//
// The chain walk needs this: re-deriving from the seal's own LastAuditHash only
// proves the seal is internally consistent, which a forger who rewrote both
// fields together satisfies. Hashing against the head the walk just computed is
// what catches a seal moved onto a different journal.
func (in SealInput) WithHead(headHash []byte) SealInput {
	in.LastAuditHash = headHash
	return in
}

// ComputeSealingHash returns the single value that stands for a whole period.
//
// Unkeyed, unlike the chain — and that is the point. An auditor holding the
// seal's published fields and the public key must be able to recompute this
// value themselves and check the signature over it, with no cooperation from
// this service. A keyed sealing hash would put us back in the position of being
// the only party able to verify our own claims.
//
// They cannot independently verify LastAuditHash, since the chain is keyed. They
// do not need to: the signature binds us to the statement "at sequence N the
// chain head was this and the derived state was that". If a later export
// presents a different head for the same period, the signature convicts us.
func ComputeSealingHash(in SealInput) []byte {
	w := &payloadWriter{}
	w.stringField(sealContext)
	w.stringField(in.PeriodID)
	w.uint64(uint64(in.FirstSequence))
	w.uint64(uint64(in.LastSequence))
	w.uint64(uint64(in.EntryCount))
	w.bytesField(in.LastAuditHash)
	w.bytesField(in.StateHash)

	sum := blake3.Sum256(w.bytes())
	return sum[:]
}

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
