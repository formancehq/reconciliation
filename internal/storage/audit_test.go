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

	// Deliberately spelled out rather than calling audit.SealInputFor: this stands
	// in for an auditor's own reimplementation, and is the guard that a field
	// added to the shared mapping cannot quietly change what a published seal
	// commits to. Collapsing it into the helper would make production code verify
	// itself against itself and remove the only independent check there is.
	recomputed := audit.ComputeSealingHash(audit.SealInput{
		PeriodID:      seal.PeriodID,
		FirstSequence: seal.FirstSequence,
		LastSequence:  seal.LastSequence,
		EntryCount:    seal.EntryCount,
		LastAuditHash: seal.LastAuditHash,
		StateHash:     seal.StateHash,

		AlertCount:      seal.AlertCount,
		UnresolvedCount: seal.UnresolvedCount,
		SealedBy:        seal.SealedBy,
		SealedAt:        seal.SealedAt,
	})
	require.Equal(t, seal.SealingHash, recomputed)
	require.True(t, store.SigningKey().Verify(recomputed, seal.Signature))
}

// A period sealed before the journal has any entries commits to boundary
// sequence 0. The walk re-derives a seal only when it reaches the entry at that
// boundary, and there is no entry at 0 — so this seal used to be the one thing a
// full verification silently skipped, and editing it returned OK from the exact
// call an auditor leans on hardest. Verified live before fixing: entry_count went
// 0 -> 99 and POST /audit-entries/verify {} still said intact.
func TestFullVerificationChecksASealWithNoEntriesBelowIt(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	var seal *models.PeriodSeal
	require.NoError(t, store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
		var err error
		seal, err = scoped.SealPeriod(ctx, SealPeriodInput{PeriodID: "2026-05"})
		return err
	}))
	require.Equal(t, int64(0), seal.LastSequence, "sealing an empty journal closes at boundary 0")
	require.Equal(t, int64(0), seal.EntryCount)

	// Intact: the seal is reported as checked, not quietly skipped. Without that
	// the caller cannot tell verification covered it.
	clean, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, clean.OK)
	require.Contains(t, clean.SealsCrossed, "2026-05")

	// Altered: every field the sealing hash covers must break the full walk.
	for _, tc := range []struct {
		name string
		set  string
	}{
		{"entryCount", "entry_count = 99"},
		{"lastSequence", "last_sequence = 0, entry_count = 1"},
		{"stateHash", `state_hash = '\x00'::bytea`},
		{"periodID", "period_id = '2026-06'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			require.NoError(t, store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
				_, err := scoped.SealPeriod(ctx, SealPeriodInput{PeriodID: "2026-05"})
				return err
			}))

			disableImmutability(t, store, ctx, "period_seal")
			_, err := store.db.NewRaw(
				"UPDATE reconciliations.period_seal SET " + tc.set).Exec(ctx)
			require.NoError(t, err)

			res, err := store.VerifyChain(ctx, 0, 0)
			require.NoError(t, err)
			require.False(t, res.OK, "editing %s left full verification reporting intact", tc.name)
			require.Equal(t, models.ChainViolationHashMismatch, res.Violation)
			require.NotNil(t, res.AtSequence, "the report must name a sequence to start from")
		})
	}

	// firstSequence is absent from the table above because the schema already
	// makes it unreachable on a genesis seal: last_sequence >= first_sequence - 1
	// pins first_sequence to 1 whenever the boundary is 0. Asserted rather than
	// assumed, so dropping the constraint reopens the hole loudly.
	t.Run("firstSequence is refused by the range constraint", func(t *testing.T) {
		store := newStore(t)
		require.NoError(t, store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
			_, err := scoped.SealPeriod(ctx, SealPeriodInput{PeriodID: "2026-05"})
			return err
		}))
		disableImmutability(t, store, ctx, "period_seal")
		_, err := store.db.NewRaw(
			"UPDATE reconciliations.period_seal SET first_sequence = 7").Exec(ctx)
		require.ErrorContains(t, err, "period_seal_range_chk")
	})
}

// The figures and attribution a seal publishes must be covered by its signature,
// not merely stored beside it. Reproduced live before fixing: setting alert_count
// and unresolved_count to 0 on a period that had one open alert left both
// /periods/{id}/verify and the full chain walk answering ok, so a report could
// cite "0 unresolved" from a response that presented itself as verified.
func TestForgedSealMetadataFailsVerification(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()

	for _, tc := range []struct {
		name string
		set  string
	}{
		{"alertCount", "alert_count = 0"},
		{"unresolvedCount", "unresolved_count = 0"},
		{"sealedBy", `sealed_by = '{"subject":"someone-else"}'::jsonb`},
		{"sealedAt", "sealed_at = sealed_at + interval '1 hour'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newStore(t)
			rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
			ev := auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)
			// A real alert, so alertCount and unresolvedCount are non-zero and
			// setting them to 0 is a genuine restatement rather than a no-op.
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

			seal := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})
			require.Positive(t, seal.AlertCount, "the fixture must produce a countable alert")
			require.Positive(t, seal.UnresolvedCount)

			ok, _, err := store.VerifySealSignature(ctx, seal)
			require.NoError(t, err)
			require.True(t, ok, "the untouched seal must verify")

			disableImmutability(t, store, ctx, "period_seal")
			_, err = store.db.NewRaw(
				"UPDATE reconciliations.period_seal SET " + tc.set + " WHERE period_id = '2026-05'").Exec(ctx)
			require.NoError(t, err)

			edited, err := store.GetPeriodSeal(ctx, "2026-05")
			require.NoError(t, err)

			ok, reason, err := store.VerifySealSignature(ctx, edited)
			require.NoError(t, err)
			require.False(t, ok, "editing %s left the seal verifying", tc.name)
			require.NotEmpty(t, reason)

			integrity, _ := store.VerifySealIntegrity(edited)
			require.False(t, integrity, "editing %s left the seal's own hash reproducing", tc.name)
		})
	}
}

// A seal's range continues from the previous seal's end, so sealing a period that
// begins earlier than the last sealed one gives it the entries recorded after
// that seal. Reproduced live: 2026-06 sealed after 2026-08 took range 8-8, so
// June attested the entry that recorded August's closure. Irreversible, which is
// why it has to be refused up front.
func TestSealPeriodRefusesGoingBackwards(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// The clock is pinned so the test does not depend on today's date: sealing a
	// period that has not begun is refused by its own guard.
	seal := func(periodID string) error {
		return store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
			_, err := tx.SealPeriod(ctx, SealPeriodInput{
				PeriodID: periodID,
				SealedBy: models.Subject{Subject: "controller"},
				At:       time.Date(2026, 12, 31, 12, 0, 0, 0, time.UTC),
			})
			return err
		})
	}
	require.NoError(t, seal("2026-08"))

	// 2026-08-15 is in the list on purpose: it begins *after* 2026-08 does, so a
	// start-versus-start rule would let a day nested inside the closed month
	// through. The books are closed through 1 September, and that is the bound.
	for _, earlier := range []string{"2026-07", "2026-06", "2026-W12", "2026-08-15", "2026-08"} {
		err := seal(earlier)
		// 2026-08 itself is refused as already sealed; the rest as backwards.
		if earlier == "2026-08" {
			require.ErrorIs(t, err, ErrPeriodAlreadySealed)
			continue
		}
		require.ErrorIs(t, err, ErrPeriodNotSealable, "%q falls inside the closed calendar", earlier)
		require.ErrorContains(t, err, "the books are closed through",
			"the error must say why, since the operator cannot undo a wrong seal")
	}

	// Forward still works, and the partition stays contiguous.
	require.NoError(t, seal("2026-09"))
	next, err := store.GetPeriodSeal(ctx, "2026-09")
	require.NoError(t, err)
	prev, err := store.GetPeriodSeal(ctx, "2026-08")
	require.NoError(t, err)
	require.Equal(t, prev.LastSequence+1, next.FirstSequence, "seals must partition with no gap")
}

// The mirror of TestSealPeriodRefusesGoingBackwards. With no seal yet there is no
// closed calendar to compare against, so sealing a period later than the entries
// already in the journal used to succeed — reproduced: sealing 2026-06 took range
// 1..2, swallowing a 2026-05 evaluation, after which 2026-05 was refused as
// backwards and could never be sealed at all.
func TestSealPeriodRefusesSkippingPastAnOpenEarlierPeriod(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationFail)

	for _, later := range []string{"2026-06", "2026-07", "2026-06-15"} {
		err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
			_, err := tx.SealPeriod(ctx, SealPeriodInput{PeriodID: later})
			return err
		})
		require.ErrorIs(t, err, ErrPeriodNotSealable, "%q skips past the open 2026-05", later)
		require.ErrorContains(t, err, "2026-05",
			"the error must name the period to seal first, since guessing wrong is permanent")
	}

	// The earliest open period is sealable, and then the next one is too — the
	// ordinary flow, where the second range legitimately contains the first's seal
	// entry tagged with the earlier period.
	may := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})
	require.Positive(t, may.EntryCount)

	june := sealPeriod(t, store, ctx, "2026-06", models.Subject{Subject: "controller"})
	require.Equal(t, may.LastSequence+1, june.FirstSequence, "seals must partition with no gap")
}

// Neither the backwards guard nor the skip-forward guard catches a first seal for
// a period that has not started: there is no earlier seal and no period-tagged
// entry to compare against. Reproduced: sealing 2099-01 on a quiet journal
// succeeded, closed the books through February 2099, and left the real 2026-08
// refused as backwards — permanently unsealable.
func TestSealPeriodRefusesAPeriodThatHasNotBegun(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	for _, future := range []string{"2099-01", "2026-09", "2026-08-22", "2026-W40"} {
		err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
			_, err := tx.SealPeriod(ctx, SealPeriodInput{PeriodID: future, At: now})
			return err
		})
		require.ErrorIs(t, err, ErrPeriodNotSealable, "%q has not begun", future)
		require.ErrorContains(t, err, "has not happened yet")
	}

	// A period already under way is still sealable: whether closing a live period
	// should be refused up front is an open question, not settled by this guard.
	live := sealPeriod(t, store, ctx, "2026-08", models.Subject{Subject: "controller"})
	require.Equal(t, "2026-08", live.PeriodID)
}

// A continuous rule and a periodic rule share one journal, and closing the
// periodic one must not freeze the continuous one. Verified rather than assumed,
// because two separate guards have to agree for it to hold: the write barrier is
// keyed on periodID and "continuous" is never sealed, and the skip-forward check
// skips ids no cadence orders — had it treated "continuous" as an open earlier
// period instead, the first weekly seal would have been refused and every one
// after it, for as long as any continuous rule existed.
//
// The asymmetry this pins is deliberate and worth stating: the seal's *range*
// covers every entry in the journal, continuous ones included, so they are
// attested; its *figures* count only alerts carrying the sealed period's id. So
// entryCount and alertCount do not reconcile by eye, and should not.
func TestSealingAPeriodLeavesContinuousAlertsAlone(t *testing.T) {
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
	openCase(cont, models.ContinuousPeriod, "asset:GBP/2")

	seal := sealPeriod(t, store, ctx, "2026-W20", models.Subject{Subject: "controller"})

	// The range takes the whole journal; the figures take only the week's cases.
	require.Equal(t, int64(8), seal.EntryCount, "the range covers the continuous entries too")
	require.Equal(t, int64(1), seal.AlertCount, "only the weekly case is counted")
	require.Equal(t, int64(1), seal.UnresolvedCount)

	// The continuous rule is untouched: existing cases still transition...
	_, err := store.AckAlert(ctx, continuousCase.ID, &models.Ack{By: "ops", At: time.Now().UTC()})
	require.NoError(t, err, "closing a weekly period must not freeze a continuous alert")

	// ...and new ones still open.
	openCase(cont, models.ContinuousPeriod, "asset:CHF/2")

	// And the next week still seals, continuing the partition with no gap.
	openCase(weekly, "2026-W21", "asset:USD/2")
	next := sealPeriod(t, store, ctx, "2026-W21", models.Subject{Subject: "controller"})
	require.Equal(t, seal.LastSequence+1, next.FirstSequence)
	require.Equal(t, int64(1), next.AlertCount)
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
	sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

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
func TestVerifyChainHandlesAnEmptySealedRange(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	// Seal before anything at all has been journalled.
	seal := sealPeriod(t, store, ctx, "2026-04", models.Subject{Subject: "controller"})
	require.Equal(t, int64(1), seal.FirstSequence)
	require.Equal(t, int64(0), seal.LastSequence, "an empty range is last = first - 1")
	require.Zero(t, seal.EntryCount)

	// Activity afterwards must not be attributed to the empty period.
	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)

	// Storage still reports the whole journal as intact.
	verification, err := store.VerifyChain(ctx, 0, 0)
	require.NoError(t, err)
	require.True(t, verification.OK)

	// The empty range genuinely precedes everything: the seal's own entry is the
	// first thing in the journal, so [firstSequence, lastSequence] = [1, 0]
	// encloses nothing. This is the property the API's short-circuit relies on.
	//
	// Asserted directly rather than through ListAuditEntries, whose ToSeq of 0
	// means "no upper bound" — a sensible convention for a query-param filter, but
	// one that cannot express an empty range.
	first, err := store.GetAuditEntry(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, models.AuditPeriodSealed, first.Kind)
	require.Equal(t, "2026-04", first.PeriodID)
	require.Equal(t, seal.AuditSequence, first.Sequence)

	inRange, err := store.db.NewSelect().Model((*models.AuditEntry)(nil)).
		Where("sequence >= ?", seal.FirstSequence).
		Where("sequence <= ?", seal.LastSequence).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, inRange, "an empty sealed range must enclose no entries")
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

// A typo'd period id is not recoverable: sealing advances a global boundary and
// the seal is immutable, so "2026-5" would consume the range belonging to
// "2026-05" and leave those entries attested under a label nobody looks up.
// Neither seal could be corrected afterwards, so the only safe place to catch it
// is before the first one is written.
func TestSealPeriodRejectsMalformedPeriodIDs(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	for _, bad := range []string{"2026-5", "26-05", "2026-05-", "may", "2026_05", "2026-W1", "  2026-05"} {
		err := store.RunInTx(ctx, func(ctx context.Context, tx *Storage) error {
			_, err := tx.SealPeriod(ctx, SealPeriodInput{PeriodID: bad})
			return err
		})
		require.ErrorIs(t, err, ErrPeriodNotSealable, "should have rejected %q", bad)
	}

	// The three shapes the cadences actually produce are accepted — over disjoint
	// calendar, and in order. Sealing refuses to reach back into closed books, so
	// 2026-W12 (16-23 March) must come before 2026-05, and the daily has to fall
	// after May closes rather than inside it. That the shapes cannot overlap is
	// the point of the partition, not a limitation of this test.
	for _, good := range []string{"2026-W12", "2026-05", "2026-06-15"} {
		require.NotPanics(t, func() {
			sealPeriod(t, store, ctx, good, models.Subject{Subject: "controller"})
		}, "should have accepted %q", good)
	}
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
	first := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})
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
	ok, reason, err := rotated.VerifySealSignature(ctx, first)
	require.NoError(t, err)
	require.True(t, ok, reason)

	// A seal signed after the rotation uses the new key.
	second := sealPeriod(t, rotated, ctx, "2026-06", models.Subject{Subject: "controller"})
	require.Equal(t, fresh.ID, second.SigningKeyID)
	ok, reason, err = rotated.VerifySealSignature(ctx, second)
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

func TestListPeriodSealsOrdersMostRecentFirst(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})
	auditTestEvaluation(t, store, ctx, rule, "2026-06", models.EvaluationPass)
	sealPeriod(t, store, ctx, "2026-06", models.Subject{Subject: "controller"})

	seals, err := store.ListPeriodSeals(ctx)
	require.NoError(t, err)
	require.Len(t, seals, 2)
	require.Equal(t, "2026-06", seals[0].PeriodID, "most recently sealed first")
	require.Equal(t, "2026-05", seals[1].PeriodID)
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
	sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

	before, err := store.db.NewSelect().Model((*models.AuditEntry)(nil)).Count(ctx)
	require.NoError(t, err)
	require.Positive(t, before)

	for _, table := range []string{"audit_entry", "rule_revision", "period_seal"} {
		_, err := store.db.NewRaw("TRUNCATE reconciliations." + table).Exec(ctx)
		require.ErrorContains(t, err, "append-only", "TRUNCATE on %s must be refused", table)
		// And the guard names the table the operator actually touched, rather than
		// always blaming audit_entry.
		require.ErrorContains(t, err, table)
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
func TestVerifySealDistinguishesUnsignedFromStripped(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	seal := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})
	require.NotEmpty(t, seal.Signature)

	// Signature removed, key id retained: tampering.
	stripped := *seal
	stripped.Signature = nil
	ok, reason, err := store.VerifySealSignature(ctx, &stripped)
	require.NoError(t, err)
	require.False(t, ok)
	require.Contains(t, reason, "was removed")
	require.Contains(t, reason, seal.SigningKeyID)

	// Neither signature nor key id: nothing was configured to sign it.
	unsigned := *seal
	unsigned.Signature = nil
	unsigned.SigningKeyID = ""
	ok, reason, err = store.VerifySealSignature(ctx, &unsigned)
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
func TestVerifySealIntegrityCatchesAnEditedSeal(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	store := newStore(t)

	rule := auditTestRule(t, store, ctx, models.CadenceMonthly)
	auditTestEvaluation(t, store, ctx, rule, "2026-05", models.EvaluationPass)
	seal := sealPeriod(t, store, ctx, "2026-05", models.Subject{Subject: "controller"})

	ok, reason := store.VerifySealIntegrity(seal)
	require.True(t, ok, reason)

	// Any field that enters the sealing hash must break it.
	for name, mutate := range map[string]func(*models.PeriodSeal){
		"entryCount":    func(s *models.PeriodSeal) { s.EntryCount = 999 },
		"lastSequence":  func(s *models.PeriodSeal) { s.LastSequence += 1 },
		"stateHash":     func(s *models.PeriodSeal) { s.StateHash = []byte("other") },
		"periodID":      func(s *models.PeriodSeal) { s.PeriodID = "2026-06" },
		"lastAuditHash": func(s *models.PeriodSeal) { s.LastAuditHash = []byte("other") },
	} {
		t.Run(name, func(t *testing.T) {
			edited := *seal
			mutate(&edited)
			ok, reason := store.VerifySealIntegrity(&edited)
			require.False(t, ok, "%s is not bound into the sealing hash", name)
			require.Contains(t, reason, "no longer reproduces")
		})
	}
}
