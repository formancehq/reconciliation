// Package memstate is the in-memory reference adapter of the controlstate
// port. It defines the expected behavior for the contract suite and serves
// as the storage double in service tests.
//
// It keeps idempotency outcomes forever. Real adapters keep them for a
// retention window (24 hours on Ledger v3).
package memstate

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/formancehq/reconciliation/internal/controlstate"
)

type entity struct {
	version  controlstate.Version
	fields   map[string]string
	balances map[string]int64
	live     map[string]string
}

type outcome struct {
	fingerprint [sha256.Size]byte
	versions    controlstate.Versions
	err         error
}

// Store is an in-memory controlstate.ControlState. The zero value is not
// usable: call New.
type Store struct {
	mu       sync.Mutex
	entities map[controlstate.Key]entity
	records  map[controlstate.Key][]controlstate.Record
	outcomes map[string]outcome
}

var _ controlstate.ControlState = (*Store)(nil)

// New returns an empty store.
func New() *Store {
	return &Store{
		entities: map[controlstate.Key]entity{},
		records:  map[controlstate.Key][]controlstate.Record{},
		outcomes: map[string]outcome{},
	}
}

// Load implements controlstate.ControlState.
func (s *Store) Load(ctx context.Context, keys ...controlstate.Key) (controlstate.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make(controlstate.Snapshot, len(keys))
	for _, k := range keys {
		e := s.entities[k]
		snap[k] = controlstate.Entry{Key: k, Version: e.version, Fields: maps.Clone(e.fields), Balances: nonEmpty(e.balances), Live: maps.Clone(e.live)}
	}
	return snap, nil
}

// Commit implements controlstate.ControlState.
func (s *Store) Commit(ctx context.Context, uow controlstate.UnitOfWork) (controlstate.Versions, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := uow.Validate(); err != nil {
		return nil, err
	}
	fp, err := fingerprint(uow)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if prev, seen := s.outcomes[uow.IdempotencyKey]; seen {
		if prev.fingerprint != fp {
			return nil, fmt.Errorf("%w: %s", controlstate.ErrKeyReused, uow.IdempotencyKey)
		}
		return maps.Clone(prev.versions), prev.err
	}

	var versions controlstate.Versions
	err = s.apply(uow)
	if err == nil {
		versions = uow.Versions()
	}
	s.outcomes[uow.IdempotencyKey] = outcome{fingerprint: fp, versions: versions, err: err}
	return maps.Clone(versions), err
}

// apply checks every precondition, then writes. The caller holds s.mu.
func (s *Store) apply(uow controlstate.UnitOfWork) error {
	for _, p := range uow.Expect {
		if got := s.entities[p.Key].version; got != p.Version {
			return fmt.Errorf("%w: %s is at version %d, expected %d", controlstate.ErrConflict, p.Key, got, p.Version)
		}
	}
	// Check every move before writing anything: a unit given back must be held.
	for _, w := range uow.Put {
		for asset, d := range w.Move {
			if got := s.entities[w.Key].balances[asset]; got+d < 0 {
				return fmt.Errorf("%w: %s holds %d %s, cannot give back %d", controlstate.ErrConflict, w.Key, got, asset, -d)
			}
		}
	}
	for _, w := range uow.Put {
		prev := s.entities[w.Key]
		balances := maps.Clone(prev.balances)
		for asset, d := range w.Move {
			if balances == nil {
				balances = map[string]int64{}
			}
			balances[asset] += d
		}
		s.entities[w.Key] = entity{
			version:  prev.version + 1,
			fields:   maps.Clone(w.Fields),
			balances: balances,
			live:     prev.live,
		}
	}
	for _, b := range uow.Bumps {
		e := s.entities[b.Key]
		e.balances = maps.Clone(e.balances)
		if e.balances == nil {
			e.balances = map[string]int64{}
		}
		for asset, d := range b.Add {
			e.balances[asset] += d
		}
		e.live = maps.Clone(e.live)
		for k, v := range b.Live {
			if e.live == nil {
				e.live = map[string]string{}
			}
			e.live[k] = v
		}
		s.entities[b.Key] = e
	}
	for _, r := range uow.Records {
		r.Fields = maps.Clone(r.Fields)
		s.records[r.Subject] = append(s.records[r.Subject], r)
	}
	return nil
}

// Records returns the records committed about subject, oldest first.
func (s *Store) Records(ctx context.Context, subject controlstate.Key) ([]controlstate.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]controlstate.Record, 0, len(s.records[subject]))
	for _, r := range s.records[subject] {
		r.Fields = maps.Clone(r.Fields)
		out = append(out, r)
	}
	return out, nil
}

// fingerprint hashes the content of a unit of work, without its key.
// Preconditions and writes are sets, so their order does not count. Records
// keep their order, which is the order they are appended in.
func fingerprint(uow controlstate.UnitOfWork) ([sha256.Size]byte, error) {
	byKey := func(a, b controlstate.Key) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	}
	expect := slices.SortedFunc(slices.Values(uow.Expect), func(a, b controlstate.Precondition) int { return byKey(a.Key, b.Key) })
	put := slices.SortedFunc(slices.Values(uow.Put), func(a, b controlstate.Write) int { return byKey(a.Key, b.Key) })
	bumps := slices.SortedFunc(slices.Values(uow.Bumps), func(a, b controlstate.Bump) int { return byKey(a.Key, b.Key) })
	// encoding/json sorts map keys, so equal fields encode equally.
	b, err := json.Marshal(struct {
		Expect  []controlstate.Precondition
		Put     []controlstate.Write
		Bumps   []controlstate.Bump
		Records []controlstate.Record
	}{expect, put, bumps, uow.Records})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("fingerprint: %w", err)
	}
	return sha256.Sum256(b), nil
}

// nonEmpty returns a copy of the positive balances, or nil when there is none.
func nonEmpty(m map[string]int64) map[string]int64 {
	var out map[string]int64
	for k, v := range m {
		if v == 0 {
			continue
		}
		if out == nil {
			out = map[string]int64{}
		}
		out[k] = v
	}
	return out
}
