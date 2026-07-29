package audit

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/zeebo/blake3"

	"github.com/formancehq/reconciliation/internal/models"
)

// HashVersion1 is the only encoding in service. The version travels on every
// entry so a future algorithm change can be introduced without invalidating the
// existing chain: a verifier picks the hasher the entry itself names.
const HashVersion1 = 1

// digestLen is the chain hash width in bytes.
const digestLen = 32

// Fields is the set of values bound into an entry's hash. Every field listed
// here is hashed — none is carried alongside the chain as "informational".
// Anything worth recording is worth binding, and a field that is stored but not
// hashed is a field an attacker may rewrite freely.
type Fields struct {
	Sequence     int64
	At           time.Time
	Kind         models.AuditEntryKind
	RuleID       *uuid.UUID
	RuleRevision *int64
	AlertID      *uuid.UUID
	EvaluationID *uuid.UUID
	PeriodID     string
	Subject      models.Subject
	Memento      []byte
}

// Chain computes hashes for a given installation. The key is derived once at
// boot and never leaves the process.
type Chain struct {
	key [32]byte
}

// NewChain builds a hasher bound to an installation key. Use DeriveChainKey to
// produce the key from the persisted salt and the optional operator pepper.
func NewChain(key [32]byte) *Chain { return &Chain{key: key} }

// Compute returns the hash for one entry, chained onto prevHash.
//
// The keying is what makes the chain non-replayable from outside: an attacker
// holding a dump of the table cannot recompute a forged entry's hash without
// the installation key, so rewriting entry N forces rewriting every entry after
// it — which is exactly what they cannot do.
//
// Genesis (the first entry) passes a nil prevHash. No genesis ceremony is
// needed because the key already separates one installation's chain from
// another's.
func (c *Chain) Compute(prevHash []byte, f Fields) ([]byte, error) {
	h, err := blake3.NewKeyed(c.key[:])
	if err != nil {
		return nil, fmt.Errorf("audit: keyed hasher: %w", err)
	}

	w := &payloadWriter{}

	// prev_hash is length-prefixed rather than raw so that a nil previous hash
	// (genesis) cannot be confused with a 32-byte one.
	w.bytesField(prevHash)

	// The sequence IS hashed. Ledger V2 deliberately left its log id out of the
	// hash (a literal `"id":0` placeholder in the trigger), which leaves a
	// renumbering detectable only by the gap it opens. V3 hashes the sequence;
	// so do we, and in Postgres it is free because the logical sequence is
	// already known before the insert.
	w.uint64(uint64(f.Sequence))

	w.uint64(uint64(f.At.UTC().UnixNano()))
	w.stringField(string(f.Kind))

	writeOptionalUUID(w, f.RuleID)
	writeOptionalInt64(w, f.RuleRevision)
	writeOptionalUUID(w, f.AlertID)
	writeOptionalUUID(w, f.EvaluationID)
	w.stringField(f.PeriodID)

	w.bytesField(EncodeSubject(f.Subject))
	w.bytesField(f.Memento)

	if _, err := h.Write(w.bytes()); err != nil {
		return nil, fmt.Errorf("audit: writing hash payload: %w", err)
	}

	out := make([]byte, digestLen)
	if _, err := h.Digest().Read(out); err != nil {
		return nil, fmt.Errorf("audit: reading digest: %w", err)
	}
	return out, nil
}

// MementoDigest is the unkeyed digest of a memento. Unkeyed on purpose: it lets
// a client that holds the memento bytes confirm the payload was not edited,
// without needing the installation key. It is a convenience, not the integrity
// guarantee — that remains the keyed chain.
func MementoDigest(memento []byte) []byte {
	sum := blake3.Sum256(memento)
	return sum[:]
}

// EncodeSubject renders the attribution snapshot as canonical bytes.
//
// The source tag is written as a byte before its value, so an action taken by a
// system component hashes differently from one taken by a caller-less request
// even when both carry an empty subject string. That is what makes a scheduled
// evaluation cryptographically distinguishable from a human intervention rather
// than distinguishable by convention.
func EncodeSubject(s models.Subject) []byte {
	w := &payloadWriter{}
	w.stringField(s.Subject)

	switch s.Source {
	case models.SubjectSourceIssuer:
		w.byteTag(0x01)
	case models.SubjectSourceClientID:
		w.byteTag(0x02)
	case models.SubjectSourceSystem:
		w.byteTag(0x03)
	default:
		w.byteTag(0x00)
	}
	w.stringField(s.SourceValue)

	scopes := make([]string, len(s.Scopes))
	copy(scopes, s.Scopes)
	sort.Strings(scopes)
	w.stringsField(scopes)

	return w.bytes()
}

// NormalizeSubject sorts the scopes in place-safe fashion so the value stored in
// jsonb matches what was hashed. Storage calls this before writing.
func NormalizeSubject(s models.Subject) models.Subject {
	if len(s.Scopes) > 1 {
		scopes := make([]string, len(s.Scopes))
		copy(scopes, s.Scopes)
		sort.Strings(scopes)
		s.Scopes = scopes
	}
	return s
}

func writeOptionalUUID(w *payloadWriter, id *uuid.UUID) {
	if id == nil {
		w.byteTag(0x00)
		return
	}
	w.byteTag(0x01)
	b := *id
	w.bytesField(b[:])
}

func writeOptionalInt64(w *payloadWriter, v *int64) {
	if v == nil {
		w.byteTag(0x00)
		return
	}
	w.byteTag(0x01)
	w.uint64(uint64(*v))
}
