package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/go-libs/v3/logging"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
)

func auditTestRule(t *testing.T, store *Storage, ctx context.Context, cadence models.Cadence) *models.Rule {
	t.Helper()
	rule := &models.Rule{
		ID:             uuid.New(),
		Name:           "audit-test-" + uuid.NewString()[:8],
		TemplateKind:   models.TemplateKind("ledger_invariant"),
		TemplateSpec:   json.RawMessage(`{"ledger":"main"}`),
		ExplanationCEL: "true",
		Enabled:        true,
		Severity:       models.Severity("high"),
		Cadence:        cadence,
	}
	require.NoError(t, store.CreateRule(ctx, rule))
	return rule
}

func auditTestEvaluation(t *testing.T, store *Storage, ctx context.Context, rule *models.Rule, periodID string, result models.EvaluationResult) *models.Evaluation {
	t.Helper()
	now := time.Now().UTC()
	ev := &models.Evaluation{
		ID:           uuid.New(),
		RuleID:       rule.ID,
		StartedAt:    now.Add(-time.Second),
		EndedAt:      now,
		PitPerSource: map[string]time.Time{"ledger": now},
		PeriodID:     periodID,
		Result:       result,
		Evidence:     json.RawMessage(`{"asset":"USD/2","drift":"0"}`),
	}
	require.NoError(t, store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		return tx.CreateEvaluation(ctx, ev)
	}))
	return ev
}

// disableImmutability drops the append-only trigger so a test can simulate
// tampering. It needs table ownership, which is exactly the point: a normal
// caller cannot do this, and the test asserts that separately.
func disableImmutability(t *testing.T, store *Storage, ctx context.Context, table string) {
	t.Helper()
	_, err := store.db.NewRaw("ALTER TABLE reconciliations." + table + " DISABLE TRIGGER USER").Exec(ctx)
	require.NoError(t, err)
}

func TestAuditChainAppendsOnEveryStateBearingWrite(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	entries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{}, 0, 100)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	require.Equal(t, models.AuditRuleCreated, entries[0].Kind)
	require.Equal(t, int64(1), entries[0].Sequence)
	require.Empty(t, entries[0].PrevHash, "genesis has no predecessor")

	require.Equal(t, models.AuditEvaluationCommitted, entries[1].Kind)
	require.Equal(t, int64(2), entries[1].Sequence)
	require.Equal(t, entries[0].Hash, entries[1].PrevHash, "the chain must link")
	require.Equal(t, "2026-05", entries[1].PeriodID)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK)
	require.Equal(t, int64(2), verification.EntriesWalked)
}

// A PASS is journalled just like a FAIL. This is the entry that answers the
// auditor's real question — prove the control ran on the days nothing was wrong.
func TestAuditChainRecordsPassingEvaluations(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	ev := auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	entries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{
		Kinds: []models.AuditEntryKind{models.AuditEvaluationCommitted},
	}, 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, ev.ID, *entries[0].EvaluationID)

	var memento audit.EvaluationMemento
	require.NoError(t, json.Unmarshal(entries[0].Memento, &memento))
	require.Equal(t, models.EvaluationPass, memento.Result)
	require.NotEmpty(t, memento.EvidenceDigest, "the evidence must be bound even on a pass")

	require.NotNil(t, ev.AuditSequence)
	require.Equal(t, entries[0].Sequence, *ev.AuditSequence)
}

func TestAuditEntriesAreImmutableThroughSQL(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	auditTestRule(t, store, ctx, models.CadenceMonthly)

	_, err := store.db.NewRaw(
		"UPDATE reconciliations.audit_entry SET kind = 'rule.deleted' WHERE sequence = 1").Exec(ctx)
	require.ErrorContains(t, err, "append-only")

	_, err = store.db.NewRaw("DELETE FROM reconciliations.audit_entry WHERE sequence = 1").Exec(ctx)
	require.ErrorContains(t, err, "append-only")

	// And the row is untouched.
	entry, err := store.GetAuditEntry(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, models.AuditRuleCreated, entry.Kind)
}

func TestVerifyChainDetectsAlteredField(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)

	// Simulate an attacker who already got past the grant and the trigger.
	disableImmutability(t, store, ctx, "audit_entry")
	_, err := store.db.NewRaw(
		"UPDATE reconciliations.audit_entry SET period_id = '2026-06' WHERE sequence = 2").Exec(ctx)
	require.NoError(t, err)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.False(t, verification.OK)
	require.Equal(t, models.ChainViolationHashMismatch, verification.Violation)
	require.NotNil(t, verification.AtSequence)
	require.Equal(t, int64(2), *verification.AtSequence)
}

func TestVerifyChainDetectsAlteredMemento(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	auditTestRule(t, store, ctx, models.CadenceMonthly)

	disableImmutability(t, store, ctx, "audit_entry")
	_, err := store.db.NewRaw(
		`UPDATE reconciliations.audit_entry SET memento = '{"tampered":true}'::bytea WHERE sequence = 1`).Exec(ctx)
	require.NoError(t, err)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.False(t, verification.OK)
	require.Equal(t, models.ChainViolationMementoDigest, verification.Violation)
}

// Deleting an entry is what the dense logical sequence exists to catch. A
// bigserial alone could not: its gaps are normal.
func TestVerifyChainDetectsDeletedEntry(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	disableImmutability(t, store, ctx, "audit_entry")
	_, err := store.db.NewRaw("DELETE FROM reconciliations.audit_entry WHERE sequence = 2").Exec(ctx)
	require.NoError(t, err)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.False(t, verification.OK)
	require.Equal(t, models.ChainViolationSequenceGap, verification.Violation)
	require.Equal(t, int64(2), *verification.AtSequence)
}

// Removing the tail leaves no interior gap, so a walk that clamped its range to
// the (now shorter) head would report a clean journal. A caller who names an
// explicit end must instead be told the entries are gone.
func TestVerifyChainDetectsTruncatedTailForAnExplicitRange(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	head, _, err := store.ChainHead(ctx)
	require.NoError(t, err)

	disableImmutability(t, store, ctx, "audit_entry")
	_, err = store.db.NewRaw("DELETE FROM reconciliations.audit_entry WHERE sequence = ?", head).Exec(ctx)
	require.NoError(t, err)

	verification, err := store.VerifyChain(ctx, 1, head)
	require.NoError(t, err)
	require.False(t, verification.OK)
	require.Equal(t, models.ChainViolationSequenceGap, verification.Violation)
	require.Contains(t, verification.Detail, "missing from the tail")
}

// The durable protection against tail truncation: a seal commits to a boundary,
// so entries removed below it are detectable even when the caller asks for no
// particular range. Past the last seal the tail is inherently unverifiable from
// the journal alone — which is the practical reason to seal promptly.
func TestVerifyChainDetectsTruncationBelowASeal(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	disableImmutability(t, store, ctx, "audit_entry")
	_, err := store.db.NewRaw(
		"DELETE FROM reconciliations.audit_entry WHERE sequence >= ?", *closure.LastSequence).Exec(ctx)
	require.NoError(t, err)

	// No range given at all — the seal is what catches it.
	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.False(t, verification.OK)
	require.Equal(t, models.ChainViolationSequenceGap, verification.Violation)
	require.Contains(t, verification.Detail, "removed from the tail")
}

func TestVerifyChainOnEmptyJournal(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK, "an empty journal is intact, not violated")
	require.Zero(t, verification.EntriesWalked)
}

func TestAuditAppendRefusesOutsideTransaction(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	_, err := store.AppendAuditEntry(ctx, AppendAuditInput{
		Kind:    models.AuditRuleCreated,
		Memento: []byte(`{}`),
	})
	require.ErrorIs(t, err, ErrAuditAppendOutsideTx)
}

func TestStorageWithoutChainRefusesJournalledWrites(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// A chainless storage must fail the write, not silently skip the journal.
	chainless := &Storage{db: store.db, pool: store.pool}
	err := chainless.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		_, err := tx.AppendAuditEntry(ctx, AppendAuditInput{
			Kind:    models.AuditRuleCreated,
			Memento: []byte(`{}`),
		})
		return err
	})
	require.ErrorIs(t, err, ErrAuditChainNotConfigured)
}

func TestRuleRevisionsAreFrozenPerRevision(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)

	newName := "renamed"
	require.NoError(t, store.PatchRule(ctx, rule.ID, RulePatch{Name: &newName}))

	revisions, err := store.ListRuleRevisions(ctx, rule.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 2)

	// Newest first.
	require.Equal(t, int64(2), revisions[0].Revision)
	require.Equal(t, "renamed", revisions[0].Name)
	require.Equal(t, int64(1), revisions[1].Revision)
	require.NotEqual(t, "renamed", revisions[1].Name,
		"the original definition must survive the rename — otherwise an old verdict is unexplainable")

	// Each revision is linked to the entry that witnessed it.
	require.Greater(t, revisions[0].AuditSequence, revisions[1].AuditSequence)
}

func TestDeleteRuleTombstonesAndKeepsHistory(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	ev := auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)

	require.NoError(t, store.DeleteRule(ctx, rule.ID))

	// Gone from the read paths.
	_, err := store.GetRule(ctx, rule.ID)
	require.ErrorIs(t, err, ErrNotFound)

	// History intact — this is what the old cascading delete destroyed.
	stored, err := store.GetEvaluation(ctx, ev.ID)
	require.NoError(t, err)
	require.Equal(t, ev.ID, stored.ID)

	entries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{RuleID: &rule.ID}, 0, 100)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, models.AuditRuleDeleted, entries[2].Kind)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK)

	// A second delete is a miss, not a second tombstone.
	require.ErrorIs(t, store.DeleteRule(ctx, rule.ID), ErrNotFound)
}

// closeJournal closes the current closure and opens its successor.
//
// No period id, because closing takes none: the range is the one the open
// closure has carried since it opened. That absence is the point of the design,
// so the helper reflects it rather than hiding it behind a parameter.
func closeJournal(t *testing.T, store *Storage, ctx context.Context, by models.Subject) *models.Closure {
	t.Helper()
	var closure *models.Closure
	err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		out, err := tx.CloseCurrentClosure(ctx, CloseClosureInput{ClosedBy: by})
		if err != nil {
			return err
		}
		closure = out
		return nil
	})
	require.NoError(t, err)
	return closure
}

// periodIn returns a closure's figures for one business period.
func periodIn(t *testing.T, closure *models.Closure, periodID string) models.ClosurePeriod {
	t.Helper()
	for _, p := range closure.Periods {
		if p.PeriodID == periodID {
			return p
		}
	}
	t.Fatalf("closure %d has no figures for period %q", closure.ID, periodID)
	return models.ClosurePeriod{}
}

func TestClosureProducesAVerifiableSignedAttestation(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	operator := models.Subject{Subject: "controller@example.com", Source: models.SubjectSourceIssuer, SourceValue: "https://issuer"}
	closure := closeJournal(t, store, ctx, operator)

	require.Equal(t, models.ClosureClosed, closure.Status)
	require.Equal(t, int64(1), closure.FirstSequence)
	require.NotNil(t, closure.LastSequence)
	require.Equal(t, int64(2), *closure.LastSequence)
	require.Equal(t, int64(2), closure.EntryCount)
	require.NotEmpty(t, closure.SealingHash)
	require.NotEmpty(t, closure.Signature, "a closure must be signed so an auditor can check it without us")
	require.Equal(t, "controller@example.com", closure.ClosedBy.Subject)

	// The business period is not lost, it is demoted: it belongs on the evidence
	// rather than on the boundary.
	may := periodIn(t, closure, "2026-05")
	require.Equal(t, int64(1), may.EntryCount, "the rule.created entry belongs to no period")

	// The closing is itself journalled, at the sequence right after the range.
	require.NotNil(t, closure.AuditSequence)
	require.Equal(t, *closure.LastSequence+1, *closure.AuditSequence)

	ok, reason, err := store.VerifyClosureSignature(ctx, closure)
	require.NoError(t, err)
	require.True(t, ok, reason)

	// And an auditor's own check, with nothing but the published fields and the
	// public key.
	keys, err := store.ListVerificationKeys(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, keys)

	// Deliberately spelled out rather than calling audit.ClosureInputFor: this
	// stands in for an auditor's own reimplementation, and is the guard that a
	// field added to the shared mapping cannot quietly change what a published
	// closure commits to. Collapsing it into the helper would make production code
	// verify itself against itself and remove the only independent check there is.
	recomputed := audit.ComputeClosureHash(audit.ClosureInput{
		ClosureID:     closure.ID,
		FirstSequence: closure.FirstSequence,
		LastSequence:  *closure.LastSequence,
		EntryCount:    closure.EntryCount,
		LastAuditHash: closure.LastAuditHash,
		StateHash:     closure.StateHash,
		ClosedBy:      closure.ClosedBy,
		ClosedAt:      *closure.ClosedAt,
	})
	require.Equal(t, closure.SealingHash, recomputed)
	require.True(t, store.SigningKey().Verify(recomputed, closure.Signature))
}

// Closing opens the successor in the same transaction. That is what removes the
// hazard a period seal had: a seal froze a period and left the next write with
// nowhere to go, which is why sealing a live period used to 409 the next
// evaluation.
// Exactly one closure is open at any time. The ledger states the same invariant
// and enforces it in a single-writer FSM; we have neither, so it is a database
// constraint rather than a convention — and worth asserting, because a second
// open closure would give two ranges the same starting sequence and quietly
// double-attest everything after it.
func TestOnlyOneClosureCanBeOpen(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	_, err := store.db.NewInsert().Model(&models.Closure{
		Status: models.ClosureOpen, OpenedAt: time.Now().UTC(), FirstSequence: 99,
	}).Exec(ctx)
	require.Error(t, err, "a second open closure must be refused by the database")
	require.ErrorContains(t, err, "closure_single_open",
		"refused by the partial unique index, not by application logic that could be bypassed")
}

// Closing twice in a row is legal and produces an empty second closure, where
// sealing the same period twice used to be a 409.
//
// The conflict is gone because what made it one is gone: a second seal of the
// same period either contradicted the first or did nothing, whereas a second
// closing attests a second, genuinely empty segment. An empty closure is a real
// answer — "we ran the controls and nothing happened" — so refusing it here would
// mean refusing it everywhere.
func TestClosingTwiceProducesAnEmptySecondClosure(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	first := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.Positive(t, first.EntryCount)

	second := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.Zero(t, second.EntryCount)
	require.Empty(t, second.Periods)
	require.NotEmpty(t, second.Signature, "an empty closure is still signed evidence")

	// And both remain verifiable, which is what makes the extra closure harmless
	// rather than noise that breaks something.
	result, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, result.OK, result.Detail)
}

func TestClosingOpensItsSuccessorAtomically(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	first := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	next, err := store.CurrentClosure(ctx)
	require.NoError(t, err)
	require.Equal(t, models.ClosureOpen, next.Status)
	require.Equal(t, *first.AuditSequence+1, next.FirstSequence,
		"the successor continues one past the closing entry: no gap, no overlap")

	// And the journal keeps accepting writes with no further ceremony.
	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationFail)
	second := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.Equal(t, next.ID, second.ID)
	require.Equal(t, next.FirstSequence, second.FirstSequence)
}

// Closures partition the journal exactly: every entry falls in one, and the
// boundaries meet without gap or overlap.
func TestClosuresPartitionTheChain(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)
	one := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationFail)
	two := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	require.Equal(t, *one.LastSequence+2, two.FirstSequence,
		"one sequence apart: the closing entry of the first sits between them")
	require.Equal(t, *one.AuditSequence+1, two.FirstSequence)
}

// A closure that covered nothing is still worth attesting: "we ran the controls
// and nothing happened" is an audit answer.
func TestClosingWithNoActivityStillAttests(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.Equal(t, int64(0), closure.EntryCount)
	require.Empty(t, closure.Periods)
	require.NotEmpty(t, closure.SealingHash)
	require.NotEmpty(t, closure.Signature)

	ok, reason, err := store.VerifyClosureSignature(ctx, closure)
	require.NoError(t, err)
	require.True(t, ok, reason)
}

// A closure that closed while the journal was still empty commits to boundary
// sequence 0, and the walk re-derives a closure only on reaching the entry at its
// boundary — of which there is none at 0. Without an explicit check it was the
// one thing a full verification silently skipped.
func TestFullVerificationChecksAClosureWithNoEntriesBelowIt(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.Equal(t, int64(0), *closure.LastSequence)

	clean, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, clean.OK)
	require.NotEmpty(t, clean.SealsCrossed, "the closure must be reported as checked, not skipped")

	for _, tc := range []struct {
		name string
		set  string
	}{
		{"entryCount", "entry_count = 99"},
		{"stateHash", `state_hash = '\x00'::bytea`},
		{"periods", `periods = '[{"periodID":"2026-05","entryCount":9,"alertCount":9,"unresolvedCount":0,"stateHash":"","ended":true,"frozen":true}]'::jsonb`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

			disableImmutability(t, store, ctx, "closure")
			_, err := store.db.NewRaw(
				"UPDATE reconciliations.closure SET " + tc.set + " WHERE status = 'CLOSED'").Exec(ctx)
			require.NoError(t, err)

			res, err := store.VerifyChain(ctx, 0, 0)
			require.NoError(t, err)
			require.False(t, res.OK, "editing %s left full verification reporting intact", tc.name)
			require.Equal(t, models.ChainViolationHashMismatch, res.Violation)
		})
	}
}

// The figures a closure publishes must be covered by its signature, not merely
// stored beside it. The breakdown enters the sealing hash only through the state
// hash, so editing a period's count inside the jsonb would otherwise leave the
// sealing hash reproducing perfectly.
func TestForgedClosureFiguresFailVerification(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()

	for _, tc := range []struct {
		name string
		set  string
	}{
		{"alertCount", `periods = jsonb_set(periods, '{0,alertCount}', '0')`},
		{"unresolvedCount", `periods = jsonb_set(periods, '{0,unresolvedCount}', '0')`},
		{"frozen", `periods = jsonb_set(periods, '{0,frozen}', 'false')`},
		{"closedBy", `closed_by = '{"subject":"someone-else"}'::jsonb`},
		{"closedAt", "closed_at = closed_at + interval '1 hour'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newStore(t)
			rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
			ev := auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)
			_, err := store.OpenOrUpdateAlert(ctx, OpenAlertInput{
				RuleID:       rule.ID,
				Fingerprint:  "asset:USD/2",
				PeriodID:     "2026-05",
				Severity:     models.Severity("high"),
				EvaluationID: ev.ID,
				Evidence:     json.RawMessage(`{"drift":"10.00"}`),
				OccurredAt:   time.Now().UTC(),
			})
			require.NoError(t, err)

			closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
			require.Positive(t, periodIn(t, closure, "2026-05").AlertCount)

			ok, _, err := store.VerifyClosureSignature(ctx, closure)
			require.NoError(t, err)
			require.True(t, ok, "the untouched closure must verify")

			disableImmutability(t, store, ctx, "closure")
			_, err = store.db.NewRaw(
				"UPDATE reconciliations.closure SET "+tc.set+" WHERE id = ?", closure.ID).Exec(ctx)
			require.NoError(t, err)

			edited, err := store.GetClosure(ctx, closure.ID)
			require.NoError(t, err)

			ok, reason, err := store.VerifyClosureSignature(ctx, edited)
			require.NoError(t, err)
			require.False(t, ok, "editing %s left the closure verifying", tc.name)
			require.NotEmpty(t, reason)
		})
	}
}

// Weekly and monthly rules can now both be attested by the same closing, which a
// period seal could not do: two seals could not cover the same stretch of
// calendar, so an installation had to pick one granularity and keep it. A closure
// attests a range and reports every period it observed, so the question does not
// arise.
func TestOneClosingAttestsEveryCadenceItObserved(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	monthly := auditTestRule(t, store, ctx, models.CadenceMonthly)
	weekly := auditTestRule(t, store, ctx, models.CadenceWeekly)
	daily := auditTestRule(t, store, ctx, models.CadenceDaily)
	auditTestEvaluation(t, store, ctx, monthly, "2026-05", models.EvaluationPass)
	auditTestEvaluation(t, store, ctx, weekly, "2026-W20", models.EvaluationPass)
	auditTestEvaluation(t, store, ctx, daily, "2026-05-15", models.EvaluationFail)

	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	require.Len(t, closure.Periods, 3)
	for _, id := range []string{"2026-05", "2026-05-15", "2026-W20"} {
		require.Equal(t, int64(1), periodIn(t, closure, id).EntryCount, "period %s", id)
	}
	// Sorted, because the state hash is taken over this sequence and a digest over
	// a set is only meaningful if the set has an agreed order.
	require.Equal(t, "2026-05", closure.Periods[0].PeriodID)
	require.Equal(t, "2026-05-15", closure.Periods[1].PeriodID)
	require.Equal(t, "2026-W20", closure.Periods[2].PeriodID)
}

// A continuous rule and a periodic rule share one journal, and closing must not
// freeze the continuous one. Two things have to agree for this to hold: the
// barrier is keyed on the period label, and continuous never ends so it is never
// frozen.
func TestClosingLeavesContinuousAlertsAlone(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	cont := auditTestRule(t, store, ctx, models.CadenceContinuous)
	weekly := auditTestRule(t, store, ctx, models.CadenceWeekly)

	openCase := func(rule *models.Rule, periodID, fingerprint string) *models.Alert {
		t.Helper()
		ev := auditTestEvaluation(t, store, ctx, rule, periodID, models.EvaluationFail)
		out, err := store.OpenOrUpdateAlert(ctx, OpenAlertInput{
			RuleID:       rule.ID,
			Fingerprint:  fingerprint,
			PeriodID:     periodID,
			Severity:     models.Severity("high"),
			EvaluationID: ev.ID,
			Evidence:     json.RawMessage(`{"drift":"1"}`),
			OccurredAt:   time.Now().UTC(),
		})
		require.NoError(t, err)
		return out.Alert
	}

	continuousCase := openCase(cont, models.ContinuousPeriod, "asset:EUR/2")
	openCase(weekly, "2026-W20", "asset:USD/2")

	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	require.True(t, periodIn(t, closure, "2026-W20").Frozen, "a period that is over is frozen")
	require.False(t, periodIn(t, closure, models.ContinuousPeriod).Frozen,
		"continuous has no end, so a closing never freezes it")

	// Existing continuous cases still transition, and new ones still open.
	_, err := store.AckAlert(ctx, continuousCase.ID, &models.Ack{By: "ops", At: time.Now().UTC()})
	require.NoError(t, err, "closing must not freeze a continuous alert")
	openCase(cont, models.ContinuousPeriod, "asset:CHF/2")
}

// A period still in progress is attested but not frozen. This is what makes
// closing a live period harmless, where sealing one used to make its rules
// unevaluatable until the next period opened.
func TestClosingDoesNotFreezeAPeriodStillInProgress(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// A period far enough ahead that it cannot have ended by the time this runs.
	future := "2999-01"
	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, future, models.EvaluationFail)

	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	attested := periodIn(t, closure, future)
	require.False(t, attested.Ended)
	require.False(t, attested.Frozen, "a period that has not ended keeps accepting writes")

	frozen, err := store.IsPeriodFrozen(ctx, future)
	require.NoError(t, err)
	require.False(t, frozen)
}

func TestVerifyChainCrossesClosuresAndRederivesThem(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)
	closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationFail)
	closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	result, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, result.OK, result.Detail)
	require.Len(t, result.SealsCrossed, 2)
}

func TestVerifyChainDetectsATamperedClosure(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)
	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationFail)

	disableImmutability(t, store, ctx, "closure")
	_, err := store.db.NewRaw(
		"UPDATE reconciliations.closure SET entry_count = entry_count + 1 WHERE id = ?", closure.ID).Exec(ctx)
	require.NoError(t, err)

	result, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.False(t, result.OK)
	require.Equal(t, models.ChainViolationHashMismatch, result.Violation)
}

func TestAuditFiltersSeparateMachineFromHuman(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// A control created by a person.
	humanCtx := audit.WithSubject(ctx, models.Subject{
		Subject: "alice@example.com", Source: models.SubjectSourceIssuer, SourceValue: "https://issuer",
	})
	rule := auditTestRule(t, store, humanCtx, models.CadenceMonthly)

	// A run performed by the scheduler.
	systemCtx := audit.WithSystemSubject(ctx, audit.ComponentScheduler)
	auditTestEvaluation(t, store, systemCtx, rule, "2026-05", models.EvaluationPass)

	humanEntries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{HumanOnly: true}, 0, 100)
	require.NoError(t, err)
	require.Len(t, humanEntries, 1)
	require.Equal(t, models.AuditRuleCreated, humanEntries[0].Kind)
	require.Equal(t, "alice@example.com", humanEntries[0].Subject.Subject)

	systemEntries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{SystemOnly: true}, 0, 100)
	require.NoError(t, err)
	require.Len(t, systemEntries, 1)
	require.True(t, systemEntries[0].Subject.IsSystem())
	require.Equal(t, audit.ComponentScheduler, systemEntries[0].Subject.SourceValue)
}

func TestAuditListPaginatesInChainOrder(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	for range 5 {
		auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	}

	page, next, err := store.ListAuditEntries(ctx, AuditEntryFilters{}, 0, 3)
	require.NoError(t, err)
	require.Len(t, page, 3)
	require.Equal(t, int64(3), next)
	require.Equal(t, int64(1), page[0].Sequence)

	rest, next, err := store.ListAuditEntries(ctx, AuditEntryFilters{}, next, 10)
	require.NoError(t, err)
	require.Len(t, rest, 3)
	require.Zero(t, next, "the last page reports no continuation")
	require.Equal(t, int64(4), rest[0].Sequence)
}

func TestAlertTransitionsAreJournalledIncludingSuppressedOnes(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	evidence := json.RawMessage(`{"drift":"10.00"}`)
	at := time.Now().UTC()

	var alertID uuid.UUID
	for i := range 2 {
		ev := auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)
		result, err := store.OpenOrUpdateAlert(ctx, OpenAlertInput{
			RuleID:       rule.ID,
			Fingerprint:  "asset:USD/2",
			PeriodID:     "2026-05",
			Severity:     models.Severity("high"),
			EvaluationID: ev.ID,
			Evidence:     evidence,
			OccurredAt:   at.Add(time.Duration(i) * time.Minute),
		})
		require.NoError(t, err)
		alertID = result.Alert.ID
		if i == 1 {
			require.False(t, result.Event.Notify,
				"a repeated identical failure is suppressed from notification")
		}
	}

	entries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{
		Kinds:   []models.AuditEntryKind{models.AuditAlertTransition},
		AlertID: &alertID,
	}, 0, 100)
	require.NoError(t, err)
	require.Len(t, entries, 2, "the suppressed transition is journalled even though nobody was paged")

	var second audit.AlertTransitionMemento
	require.NoError(t, json.Unmarshal(entries[1].Memento, &second))
	require.False(t, second.Notify,
		"the suppression decision is bound into the hash, so 'we were never told' stays checkable")

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK)
}

func TestEnsureAuditChainRejectsAChangedPepper(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// newStore initialised the chain with "test-pepper".
	_, err := EnsureAuditChain(ctx, NewStorage(store.pool), AuditChainSettings{Pepper: "different"})
	require.ErrorIs(t, err, ErrAuditChainKeyMismatch)

	// The right pepper still works, and yields the same key.
	reopened, err := EnsureAuditChain(ctx, NewStorage(store.pool), AuditChainSettings{Pepper: "test-pepper"})
	require.NoError(t, err)
	require.True(t, reopened.HasAuditChain())
}

// A timestamp is hashed, so the value hashed has to be the value Postgres can
// store. timestamptz keeps microseconds while time.Now() on Linux carries
// nanoseconds, so an un-truncated hash would make every entry verify as
// tampered — in production only, since macOS wall-clock readings are already
// microsecond-granular. This test pins the behaviour with an explicit
// sub-microsecond timestamp so it fails on either platform.
func TestAuditEntryTimestampSurvivesPostgresPrecision(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	withNanos := time.Date(2026, 5, 15, 12, 0, 0, 123456789, time.UTC)
	err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		_, err := tx.AppendAuditEntry(ctx, AppendAuditInput{
			At:      withNanos,
			Kind:    models.AuditRuleCreated,
			Memento: []byte(`{"probe":true}`),
		})
		return err
	})
	require.NoError(t, err)

	entry, err := store.GetAuditEntry(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, withNanos.Truncate(time.Microsecond), entry.At.UTC(),
		"the stored timestamp must equal the truncated one, not the nanosecond original")

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK, "detail: %s", verification.Detail)
}

// --- regressions from the first review pass -------------------------------

// A passing evaluation moves no case, so it never reaches appendAlertEvent and
// used to file fresh evidence under a period whose seal had already counted its
// entries. "Everything for May" would then list more than May's seal attests to.
func TestSealedPeriodRefusesFurtherEvaluations(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	// No alert involved at all — this is the quiet path.
	now := time.Now().UTC()
	err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		return tx.CreateEvaluation(ctx, &models.Evaluation{
			ID: uuid.New(), RuleID: rule.ID, StartedAt: now, EndedAt: now,
			PeriodID: "2026-05", Result: models.EvaluationPass,
		})
	})
	require.ErrorIs(t, err, ErrPeriodSealed)

	// The next period is unaffected.
	require.NotPanics(t, func() {
		auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationPass)
	})
}

// A seal over an empty range stores lastSequence = firstSequence - 1, which for a
// first seal is 0. Verification must read that as "nothing to check", not as "no
// upper bound given" — otherwise it silently answers about a different range.
func TestVerifyChainHandlesAnEmptyClosure(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// Close before anything at all has been journalled.
	closure := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.Equal(t, int64(1), closure.FirstSequence)
	require.Equal(t, int64(0), *closure.LastSequence, "an empty range is last = first - 1")
	require.Zero(t, closure.EntryCount)
	require.Empty(t, closure.Periods, "nothing was observed, so there is nothing to break down")

	// Activity afterwards must not be attributed to the empty closure.
	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	// Storage still reports the whole journal as intact.
	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK, verification.Detail)

	// The empty range genuinely precedes everything: the closing's own entry is
	// the first thing in the journal, so [firstSequence, lastSequence] = [1, 0]
	// encloses nothing. This is the property the API's short-circuit relies on.
	//
	// Asserted directly rather than through ListAuditEntries, whose ToSeq of 0
	// means "no upper bound" — a sensible convention for a query-param filter, but
	// one that cannot express an empty range.
	first, err := store.GetAuditEntry(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, models.AuditClosureSealed, first.Kind)
	require.Equal(t, *closure.AuditSequence, first.Sequence)

	inRange, err := store.db.NewSelect().Model((*models.AuditEntry)(nil)).
		Where("sequence >= ?", closure.FirstSequence).
		Where("sequence <= ?", *closure.LastSequence).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, inRange, "an empty closure must enclose no entries")
}

// Verifying from a start whose predecessor was deleted used to skip the first
// link check and report OK — a clean bill of health for a chain broken at exactly
// the boundary the caller asked about.
func TestVerifyChainReportsAMissingPredecessor(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	disableImmutability(t, store, ctx, "audit_entry")
	_, err := store.db.NewRaw("DELETE FROM reconciliations.audit_entry WHERE sequence = 2").Exec(ctx)
	require.NoError(t, err)

	// Start at 3, whose predecessor 2 is now gone.
	verification, err := store.VerifyChain(ctx, 3, 0)
	require.NoError(t, err)
	require.False(t, verification.OK, "detail: %s", verification.Detail)
	require.Equal(t, models.ChainViolationSequenceGap, verification.Violation)
	require.Equal(t, int64(2), *verification.AtSequence)
}

// Every evaluation records the definition that produced its verdict, manual ones
// included. Without it a verdict cannot be linked to the frozen revision once the
// rule is revised — most of the point of keeping revisions.
func TestEvaluationRecordsItsRuleRevision(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	revision := rule.Revision

	now := time.Now().UTC()
	ev := &models.Evaluation{
		ID: uuid.New(), RuleID: rule.ID, StartedAt: now, EndedAt: now,
		PeriodID: "2026-05", Result: models.EvaluationPass,
		// No ScheduledAt: this is the manual path, which the schedule-identity
		// constraint used to force into a NULL revision.
		RuleRevision: &revision,
	}
	require.NoError(t, store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		return tx.CreateEvaluation(ctx, ev)
	}))

	entries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{
		Kinds: []models.AuditEntryKind{models.AuditEvaluationCommitted},
	}, 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].RuleRevision)
	require.Equal(t, revision, *entries[0].RuleRevision)

	var memento audit.EvaluationMemento
	require.NoError(t, json.Unmarshal(entries[0].Memento, &memento))
	require.NotNil(t, memento.RuleRevision, "the memento must carry the revision too")
	require.Equal(t, revision, *memento.RuleRevision)

	// And the revision it names is retrievable.
	frozen, err := store.GetRuleRevision(ctx, rule.ID, revision)
	require.NoError(t, err)
	require.Equal(t, rule.Name, frozen.Name)
}

// The config row and the signing-key row are written by separate statements. A
// crash between them left a key that signs seals but is absent from the published
// list, making every seal it signed permanently unverifiable — and later boots
// took the fast path and never repaired it.
func TestEnsureAuditChainRepublishesAMissingSigningKey(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	before, err := store.ListVerificationKeys(ctx)
	require.NoError(t, err)
	require.Len(t, before, 1)

	// Simulate the crash window: the key row never landed.
	_, err = store.db.NewRaw("DELETE FROM reconciliations.audit_signing_key").Exec(ctx)
	require.NoError(t, err)
	gone, err := store.ListVerificationKeys(ctx)
	require.NoError(t, err)
	require.Empty(t, gone)

	// A subsequent boot must repair it rather than carry on signing with a key
	// nobody can look up.
	reopened, err := EnsureAuditChain(ctx, NewStorage(store.pool), AuditChainSettings{Pepper: "test-pepper"})
	require.NoError(t, err)

	after, err := reopened.ListVerificationKeys(ctx)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, before[0].KeyID, after[0].KeyID)
	require.Equal(t, before[0].PublicKey, after[0].PublicKey)
}

// Losing the key material while keeping the journal is reachable without an
// attacker — a partial restore, or a cleanup script that truncates the wrong
// table. Minting a fresh key then would orphan every existing entry and make the
// damage indistinguishable from tampering, so boot must refuse instead.
//
// key_check cannot catch this on its own: it is regenerated with the new salt, so
// it agrees with itself.
func TestEnsureAuditChainRefusesToRekeyANonEmptyJournal(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	auditTestRule(t, store, ctx, models.CadenceMonthly)
	head, _, err := store.ChainHead(ctx)
	require.NoError(t, err)
	require.Positive(t, head)

	// The journal survives; its key material does not.
	_, err = store.db.NewRaw("DELETE FROM reconciliations.audit_chain_config").Exec(ctx)
	require.NoError(t, err)

	_, err = EnsureAuditChain(ctx, NewStorage(store.pool), AuditChainSettings{Pepper: "test-pepper"})
	require.ErrorIs(t, err, ErrAuditChainKeyLost)
	require.ErrorContains(t, err, "unverifiable")
}

// An empty journal is intact as an answer to "verify whatever is there". It is
// NOT intact as an answer to "verify sequences 1..10" — a journal truncated to
// nothing is the most complete tampering possible, and would otherwise verify
// best of all.
func TestVerifyChainDoesNotPassAnExplicitRangeAgainstAnEmptyJournal(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// No range asked for: nothing to verify, so intact.
	open, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, open.OK)

	// A specific range asked for: those entries are absent, and that is a gap.
	explicit, err := store.VerifyChain(ctx, 1, 10)
	require.NoError(t, err)
	require.False(t, explicit.OK, "detail: %s", explicit.Detail)
	require.Equal(t, models.ChainViolationSequenceGap, explicit.Violation)
	require.Contains(t, explicit.Detail, "journal is empty")
}

// Rotation must retire the previous key without orphaning the seals it signed —
// an auditor holding a two-year-old seal still has to be able to look its key up.
func TestSigningKeyRotationKeepsPriorKeysVerifiable(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	before, err := store.ListVerificationKeys(ctx)
	require.NoError(t, err)
	require.Len(t, before, 1)
	originalID := before[0].KeyID

	// Seal something with the original key.
	auditTestRule(t, store, ctx, models.CadenceMonthly)
	first := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.Equal(t, originalID, first.SigningKeyID)

	// Rotate by supplying a different seed out-of-band.
	fresh, err := audit.GenerateSigningKey()
	require.NoError(t, err)
	rotated, err := EnsureAuditChain(ctx, NewStorage(store.pool), AuditChainSettings{
		Pepper:         "test-pepper",
		SigningKeySeed: fresh.SeedBase64(),
	})
	require.NoError(t, err)
	require.Equal(t, fresh.ID, rotated.SigningKey().ID)

	keys, err := rotated.ListVerificationKeys(ctx)
	require.NoError(t, err)
	require.Len(t, keys, 2, "the retired key must still be published")

	byID := map[string]VerificationKey{}
	for _, k := range keys {
		byID[k.KeyID] = k
	}
	require.True(t, byID[fresh.ID].Active)
	require.False(t, byID[originalID].Active, "the previous key is retired")
	require.NotNil(t, byID[originalID].RetiredAt)

	// And the seal signed by the retired key still verifies.
	ok, reason, err := rotated.VerifyClosureSignature(ctx, first)
	require.NoError(t, err)
	require.True(t, ok, reason)

	// A seal signed after the rotation uses the new key.
	second := closeJournal(t, rotated, ctx, models.Subject{Subject: "controller"})
	require.Equal(t, fresh.ID, second.SigningKeyID)
	ok, reason, err = rotated.VerifyClosureSignature(ctx, second)
	require.NoError(t, err)
	require.True(t, ok, reason)
}

// The thumbprint is rendered for a blob the schema does not length-constrain, so
// a short value from a manual insert or a restored dump must not panic the
// endpoint an auditor calls.
func TestVerificationKeyThumbprintToleratesShortBlobs(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", thumbprintOf(nil))
	require.Equal(t, "ab", thumbprintOf([]byte{0xab}))
	require.Len(t, thumbprintOf(make([]byte, 32)), 16, "long keys are truncated to 8 bytes")
}

func TestListClosuresOrdersMostRecentFirst(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationPass)
	closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	closures, err := store.ListClosures(ctx)
	require.NoError(t, err)
	// Two closed plus the successor the second closing opened.
	require.Len(t, closures, 3)
	require.Equal(t, models.ClosureOpen, closures[0].Status, "most recent first, and the open one leads")
	require.Equal(t, models.ClosureClosed, closures[1].Status)
	require.Greater(t, closures[1].ID, closures[2].ID)
	require.Equal(t, "2026-06", periodIn(t, &closures[1], "2026-06").PeriodID)
	require.Equal(t, "2026-05", periodIn(t, &closures[2], "2026-05").PeriodID)
}

// A FOR EACH ROW trigger does not fire on TRUNCATE, so before the statement-level
// guard the entire journal could be emptied silently — verified against the local
// stack, five entries to zero. That falsifies the immutability claim outright,
// since removing every entry is easier than editing one.
func TestAuditJournalCannotBeTruncated(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	before, err := store.db.NewSelect().Model((*models.AuditEntry)(nil)).Count(ctx)
	require.NoError(t, err)
	require.Positive(t, before)

	for _, tc := range []struct {
		// named is the table the error must blame; target is what actually gets
		// truncated. closure has to go with frozen_period, because a foreign key
		// would otherwise refuse the statement before the trigger runs — testing
		// the wrong guard entirely.
		named, target string
	}{
		{"audit_entry", "reconciliations.audit_entry"},
		{"rule_revision", "reconciliations.rule_revision"},
		{"closure", "reconciliations.closure, reconciliations.frozen_period"},
	} {
		_, err := store.db.NewRaw("TRUNCATE " + tc.target).Exec(ctx)
		require.ErrorContains(t, err, "append-only", "TRUNCATE on %s must be refused", tc.named)
		// And the guard names the table the operator actually touched, rather than
		// always blaming audit_entry.
		require.ErrorContains(t, err, tc.named)
	}

	after, err := store.db.NewSelect().Model((*models.AuditEntry)(nil)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "nothing may be removed")

	// The chain is still verifiable, which is the property all of this protects.
	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK, "detail: %s", verification.Detail)
}

// An intentionally unsigned seal and one whose signature was stripped used to
// report identically. Only the second is an incident, so an auditor has to be able
// to tell them apart.
func TestVerifyClosureDistinguishesUnsignedFromStripped(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	seal := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})
	require.NotEmpty(t, seal.Signature)

	// Signature removed, key id retained: tampering.
	stripped := *seal
	stripped.Signature = nil
	ok, reason, err := store.VerifyClosureSignature(ctx, &stripped)
	require.NoError(t, err)
	require.False(t, ok)
	require.Contains(t, reason, "was removed")
	require.Contains(t, reason, seal.SigningKeyID)

	// Neither signature nor key id: nothing was configured to sign it.
	unsigned := *seal
	unsigned.Signature = nil
	unsigned.SigningKeyID = ""
	ok, reason, err = store.VerifyClosureSignature(ctx, &unsigned)
	require.NoError(t, err)
	require.False(t, ok)
	require.Contains(t, reason, "no signing key was available")
	require.NotContains(t, reason, "removed", "must not read as an incident")
}

// Every ruleRevision in the journal must resolve to a frozen definition. The
// deletion entry named the post-bump revision, which is never frozen — so
// GET /rules/{id}/revisions/{n} 404'd for precisely the entry an auditor is most
// likely to follow. The bump still happens on the rule row, to fence in-flight
// scheduled jobs; it just is not what the entry points at.
func TestRuleDeletionEntryNamesAResolvableRevision(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	newName := "renamed"
	require.NoError(t, store.PatchRule(ctx, rule.ID, RulePatch{Name: &newName}))
	require.NoError(t, store.DeleteRule(ctx, rule.ID))

	entries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{
		Kinds: []models.AuditEntryKind{models.AuditRuleDeleted}, RuleID: &rule.ID,
	}, 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].RuleRevision)

	// Whatever it names must be readable.
	frozen, err := store.GetRuleRevision(ctx, rule.ID, *entries[0].RuleRevision)
	require.NoError(t, err, "the deletion entry points at revision %d, which has no frozen definition", *entries[0].RuleRevision)
	require.Equal(t, "renamed", frozen.Name, "should be the definition in force at deletion")

	// The memento still records the fencing bump, so nothing is lost.
	var memento audit.RuleDeletedMemento
	require.NoError(t, json.Unmarshal(entries[0].Memento, &memento))
	require.Equal(t, *entries[0].RuleRevision, memento.Revision)
	require.Equal(t, memento.Revision+1, memento.TombstonedAtRevision)

	// Every ruleRevision in the whole journal resolves.
	all, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{RuleID: &rule.ID}, 0, 100)
	require.NoError(t, err)
	for _, e := range all {
		if e.RuleRevision == nil {
			continue
		}
		_, err := store.GetRuleRevision(ctx, rule.ID, *e.RuleRevision)
		require.NoError(t, err, "entry %d (%s) names unreadable revision %d", e.Sequence, e.Kind, *e.RuleRevision)
	}
}

// An empty sealed range has no entries to recompute, so a chain walk crosses no
// seal and re-derives nothing. The seal itself can still have been edited, which
// is what VerifySealIntegrity is for — signature aside, since an installation
// without a signing key produces unsigned seals by design.
func TestVerifyClosureIntegrityCatchesAnEditedClosure(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	seal := closeJournal(t, store, ctx, models.Subject{Subject: "controller"})

	ok, reason := store.VerifyClosureIntegrity(seal)
	require.True(t, ok, reason)

	// Any field that enters the sealing hash must break it.
	for name, mutate := range map[string]func(*models.Closure){
		"entryCount":    func(s *models.Closure) { s.EntryCount = 999 },
		"lastSequence":  func(s *models.Closure) { s.LastSequence = pointerTo(*s.LastSequence + 1) },
		"stateHash":     func(s *models.Closure) { s.StateHash = []byte("other") },
		"lastAuditHash": func(s *models.Closure) { s.LastAuditHash = []byte("other") },
	} {
		t.Run(name, func(t *testing.T) {
			edited := *seal
			mutate(&edited)
			ok, reason := store.VerifyClosureIntegrity(&edited)
			require.False(t, ok, "%s is not bound into the sealing hash", name)
			require.Contains(t, reason, "no longer reproduces")
		})
	}
}

func pointerTo[T any](v T) *T { return &v }
