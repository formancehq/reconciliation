// Package controlstate is the storage port for reconciliation's mutable
// control state: rule definitions, alert state and evaluation tokens.
//
// The service builds a UnitOfWork and an adapter commits it in one atomic
// call. The Ledger v3 adapter maps it to one ApplyBatch; a future Ledger
// key-value store maps it to compare-and-swap operations. Moving between the
// two replaces an adapter and leaves the service untouched (RFC 0022,
// docs/drafts/rfc-kv-store-control-state.md).
//
// Every adapter must pass the contract suite in package controlstatetest.
package controlstate

import (
	"context"
	"errors"
	"fmt"
)

// Errors returned by Commit. Adapters wrap them, so callers use errors.Is.
var (
	// ErrConflict means a precondition did not hold: an entity was not at the
	// expected version, or existed when it was expected absent. Nothing was
	// written. The caller re-loads, re-plans, and commits with a new key.
	ErrConflict = errors.New("controlstate: precondition failed")
	// ErrKeyReused means the idempotency key was already used by a unit of
	// work with a different content. Nothing was written. Two intents share
	// one identity: this is a bug in the caller, never a reason to retry
	// with a fresh key.
	ErrKeyReused = errors.New("controlstate: idempotency key reused with a different content")
	// ErrInvalid means the unit of work is malformed. It is detected before
	// the commit is accepted, so the idempotency key is not consumed.
	ErrInvalid = errors.New("controlstate: invalid unit of work")
)

// Key names one mutable entity.
type Key struct {
	Kind string // for example "rule", "alert", "token"
	ID   string
}

func (k Key) String() string { return k.Kind + "/" + k.ID }

// Version is the version of an entity. Version 0 means the entity does not
// exist. The first write creates version 1, and every later write adds one.
type Version uint64

// Absent is the version of an entity that does not exist.
const Absent Version = 0

// Entry is the current state of one entity.
type Entry struct {
	Key     Key
	Version Version
	Fields  map[string]string // nil when the entity is absent
}

// Exists reports whether the entity exists.
func (e Entry) Exists() bool { return e.Version != Absent }

// Snapshot holds the entries returned by one Load.
type Snapshot map[Key]Entry

// Get returns the entry for k. A key that Load did not return reads as absent.
func (s Snapshot) Get(k Key) Entry {
	if e, ok := s[k]; ok {
		return e
	}
	return Entry{Key: k}
}

// Precondition requires an entity to be at a version when the unit of work
// commits. Version Absent requires the entity not to exist.
type Precondition struct {
	Key     Key
	Version Version
}

// Expect builds the precondition that the entry is still as it was loaded.
func Expect(e Entry) Precondition { return Precondition{Key: e.Key, Version: e.Version} }

// ExpectAbsent builds the precondition that k does not exist. It must come with
// a write that creates k in the same unit of work.
func ExpectAbsent(k Key) Precondition { return Precondition{Key: k, Version: Absent} }

// Write replaces the fields of an entity. Its key must carry a precondition
// in the same unit of work, so every write proves the state it replaces.
type Write struct {
	Key    Key
	Fields map[string]string
}

// Record is an immutable fact committed with the unit of work, such as a
// capture or an activity entry. Records are appended, never replaced.
type Record struct {
	Kind    string // for example "capture", "activity"
	Subject Key    // the entity the record is about
	Fields  map[string]string
}

// Versions holds the version of every entity a unit of work wrote, as it
// stands right after that unit of work. The API exposes it as an ETag.
type Versions map[Key]Version

// UnitOfWork is committed atomically: every precondition holds and every
// write and record lands, or nothing does.
type UnitOfWork struct {
	// IdempotencyKey identifies this commit attempt. Derive it from the
	// business identity and the versions read, never from a random value.
	// Replaying the same key with the same content returns the first
	// outcome without writing again, for at least the adapter's retention
	// window (24 hours on Ledger v3). A key whose commit was rejected stays
	// consumed: re-plan with a new key.
	IdempotencyKey string
	Expect         []Precondition
	Put            []Write
	Records        []Record
}

// Validate checks the rules every adapter enforces before it accepts a unit
// of work. It returns an error wrapping ErrInvalid.
func (u UnitOfWork) Validate() error {
	if u.IdempotencyKey == "" {
		return fmt.Errorf("%w: empty idempotency key", ErrInvalid)
	}
	if len(u.Put) == 0 && len(u.Records) == 0 {
		return fmt.Errorf("%w: nothing to commit", ErrInvalid)
	}
	expected := make(map[Key]Version, len(u.Expect))
	for _, p := range u.Expect {
		if err := validKey(p.Key); err != nil {
			return err
		}
		if _, dup := expected[p.Key]; dup {
			return fmt.Errorf("%w: two preconditions on %s", ErrInvalid, p.Key)
		}
		expected[p.Key] = p.Version
	}
	written := make(map[Key]bool, len(u.Put))
	for _, w := range u.Put {
		if err := validKey(w.Key); err != nil {
			return err
		}
		if written[w.Key] {
			return fmt.Errorf("%w: two writes to %s", ErrInvalid, w.Key)
		}
		written[w.Key] = true
		if _, ok := expected[w.Key]; !ok {
			return fmt.Errorf("%w: write to %s without a precondition", ErrInvalid, w.Key)
		}
	}
	// An absent precondition only guards a create. A ledger can prove that an
	// entity exists at a version, not that it is still absent without
	// creating it, so every adapter requires the create alongside.
	for k, v := range expected {
		if v == Absent && !written[k] {
			return fmt.Errorf("%w: absent precondition on %s without a write", ErrInvalid, k)
		}
	}
	for _, r := range u.Records {
		if r.Kind == "" {
			return fmt.Errorf("%w: record without a kind", ErrInvalid)
		}
		if err := validKey(r.Subject); err != nil {
			return err
		}
	}
	return nil
}

// Versions returns the versions the writes of u produce: each written key
// moves from its expected version to the next one. Every adapter returns
// this from Commit, including on a replay.
func (u UnitOfWork) Versions() Versions {
	expected := make(map[Key]Version, len(u.Expect))
	for _, p := range u.Expect {
		expected[p.Key] = p.Version
	}
	out := make(Versions, len(u.Put))
	for _, w := range u.Put {
		out[w.Key] = expected[w.Key] + 1
	}
	return out
}

func validKey(k Key) error {
	if k.Kind == "" || k.ID == "" {
		return fmt.Errorf("%w: incomplete key %q", ErrInvalid, k)
	}
	return nil
}

// ControlState commits every precondition, write and record of a unit of
// work atomically, or none of them.
type ControlState interface {
	// Load returns the current entries of the given keys. A missing entity
	// is returned with version Absent. The returned fields are copies.
	Load(ctx context.Context, keys ...Key) (Snapshot, error)
	// Commit returns the new versions of the written entities when the unit
	// of work landed, or was already landed under the same key with the same
	// content. It returns an error wrapping ErrConflict, ErrKeyReused or
	// ErrInvalid otherwise.
	Commit(ctx context.Context, uow UnitOfWork) (Versions, error)
}
