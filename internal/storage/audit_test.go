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
	seal := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

	disableImmutability(t, store, ctx, "audit_entry")
	_, err := store.db.NewRaw(
		"DELETE FROM reconciliations.audit_entry WHERE sequence >= ?", seal.LastSequence).Exec(ctx)
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

func sealPeriod(t *testing.T, store *Storage, ctx context.Context, periodID string, by models.Subject) *models.PeriodSeal {
	t.Helper()
	var seal *models.PeriodSeal
	err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		out, err := tx.SealPeriod(ctx, SealPeriodInput{PeriodID: periodID, SealedBy: by})
		if err != nil {
			return err
		}
		seal = out
		return nil
	})
	require.NoError(t, err)
	return seal
}

func TestSealPeriodProducesAVerifiableSignedSeal(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	operator := models.Subject{Subject: "controller@example.com", Source: models.SubjectSourceIssuer, SourceValue: "https://issuer"}
	seal := sealPeriod(t, store, ctx, "2026-05", operator)

	require.Equal(t, "2026-05", seal.PeriodID)
	require.Equal(t, int64(1), seal.FirstSequence)
	require.Equal(t, int64(2), seal.LastSequence)
	require.Equal(t, int64(2), seal.EntryCount)
	require.NotEmpty(t, seal.SealingHash)
	require.NotEmpty(t, seal.Signature, "the seal must be signed so an auditor can check it without us")
	require.Equal(t, "controller@example.com", seal.SealedBy.Subject)
	require.Equal(t, models.PeriodSealed, seal.Status())

	// The seal is itself journalled, at the sequence right after the range.
	require.Equal(t, seal.LastSequence+1, seal.AuditSequence)

	ok, reason, err := store.VerifySealSignature(ctx, seal)
	require.NoError(t, err)
	require.True(t, ok, reason)

	// And an auditor's own check, with nothing but the published fields and the
	// public key.
	keys, err := store.ListVerificationKeys(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, keys)

	recomputed := audit.ComputeSealingHash(audit.SealInput{
		PeriodID:      seal.PeriodID,
		FirstSequence: seal.FirstSequence,
		LastSequence:  seal.LastSequence,
		EntryCount:    seal.EntryCount,
		LastAuditHash: seal.LastAuditHash,
		StateHash:     seal.StateHash,
	})
	require.Equal(t, seal.SealingHash, recomputed)
	require.True(t, store.SigningKey().Verify(recomputed, seal.Signature))
}

func TestSealedPeriodRefusesFurtherAlertTransitions(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	ev := auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)

	opened, err := store.OpenOrUpdateAlert(ctx, OpenAlertInput{
		RuleID:       rule.ID,
		Fingerprint:  "asset:USD/2",
		PeriodID:     "2026-05",
		Severity:     models.Severity("high"),
		EvaluationID: ev.ID,
		Evidence:     json.RawMessage(`{"drift":"10.00"}`),
		OccurredAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, opened.Alert)

	sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

	// The closing barrier: after the books are closed, the books do not move.
	_, err = store.AckAlert(ctx, opened.Alert.ID, &models.Ack{By: "someone", At: time.Now().UTC()})
	require.ErrorIs(t, err, ErrPeriodSealed)

	_, err = store.ResolveAlertManual(ctx, opened.Alert.ID, &models.Resolution{
		Kind: models.ResolutionFixedByBooking, By: "someone", At: time.Now().UTC(),
	})
	require.ErrorIs(t, err, ErrPeriodSealed)

	// A different period is unaffected.
	ev2 := auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationFail)
	next, err := store.OpenOrUpdateAlert(ctx, OpenAlertInput{
		RuleID:       rule.ID,
		Fingerprint:  "asset:USD/2",
		PeriodID:     "2026-06",
		Severity:     models.Severity("high"),
		EvaluationID: ev2.ID,
		Evidence:     json.RawMessage(`{"drift":"10.00"}`),
		OccurredAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotEqual(t, opened.Alert.ID, next.Alert.ID)
}

func TestSealPeriodIsNotIdempotent(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	auditTestRule(t, store, ctx, models.CadenceMonthly)
	sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

	err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		_, err := tx.SealPeriod(ctx, SealPeriodInput{PeriodID: "2026-05"})
		return err
	})
	require.ErrorIs(t, err, ErrPeriodAlreadySealed)
}

// Sealing the continuous pseudo-period would freeze every live-monitoring rule
// with no successor period for its alerts to move into.
func TestSealPeriodRejectsContinuous(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
		_, err := tx.SealPeriod(ctx, SealPeriodInput{PeriodID: models.ContinuousPeriod})
		return err
	})
	require.ErrorIs(t, err, ErrPeriodNotSealable)
}

// Seals partition the chain: the next one starts where the last one stopped, so
// no entry falls outside every period and none is covered twice.
func TestSealsPartitionTheChain(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	first := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationPass)
	second := sealPeriod(t, store, ctx, "2026-06", models.Subject{Subject: "controller"})

	require.Equal(t, first.LastSequence+1, second.FirstSequence)
	require.Greater(t, second.LastSequence, second.FirstSequence-1)
}

// A quiet period still gets a seal. "We ran the controls and nothing happened"
// is an audit answer, and refusing to record it would leave the operator with no
// attestation at all for that month.
//
// Its range is not literally empty: a seal's own journal entry lands after the
// boundary it describes, so it falls into the following period — the same way a
// Ledger chapter's seal order is proposed after the close sequence. So a quiet
// June covers exactly one entry, May's seal.
func TestSealPeriodWithNoActivityStillSeals(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	auditTestRule(t, store, ctx, models.CadenceMonthly)
	first := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})
	second := sealPeriod(t, store, ctx, "2026-06", models.Subject{Subject: "controller"})

	require.Equal(t, int64(1), second.EntryCount)
	require.Zero(t, second.AlertCount, "nothing happened in June")

	entries, _, err := store.ListAuditEntries(ctx, AuditEntryFilters{
		FromSeq: second.FirstSequence, ToSeq: second.LastSequence,
	}, 0, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, models.AuditPeriodSealed, entries[0].Kind,
		"the only entry in a quiet period is the previous period's seal")

	require.Equal(t, first.LastSequence+1, second.FirstSequence)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK)
}

func TestVerifyChainCrossesSealsAndRederivesThem(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})
	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationPass)

	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK)
	require.Equal(t, []string{"2026-05"}, verification.SealsCrossed)
}

func TestVerifyChainDetectsATamperedSeal(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)
	seal := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

	disableImmutability(t, store, ctx, "period_seal")
	_, err := store.db.NewRaw(
		"UPDATE reconciliations.period_seal SET unresolved_count = 0 WHERE period_id = '2026-05'").Exec(ctx)
	require.NoError(t, err)
	// The headline figure is not in the sealing hash, so that edit alone does not
	// break the chain — but the entry_count is, and so is the state hash.
	_, err = store.db.NewRaw(
		"UPDATE reconciliations.period_seal SET entry_count = 99 WHERE period_id = '2026-05'").Exec(ctx)
	require.NoError(t, err)

	verification, err := store.VerifyChain(ctx, 0, seal.LastSequence)
	require.NoError(t, err)
	require.False(t, verification.OK)
	require.Contains(t, verification.Detail, "does not match the journal")
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
