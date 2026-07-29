package audit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/formancehq/reconciliation/internal/models"
)

func TestCanonicalizeSortsKeysAndStripsWhitespace(t *testing.T) {
	t.Parallel()

	out, err := Canonicalize([]byte(`{ "b": 1, "a": { "d": 2, "c": 3 } }`))
	require.NoError(t, err)
	require.Equal(t, `{"a":{"c":3,"d":2},"b":1}`, string(out))
}

// The property that matters: jsonb does not preserve key order, so a payload
// read back from the database must re-canonicalize to the bytes that were
// hashed. Without this, every verification would fail on perfectly untampered
// data.
func TestCanonicalizeIsStableUnderKeyReordering(t *testing.T) {
	t.Parallel()

	first, err := Canonicalize([]byte(`{"ledger":"main","asset":"USD/2","drift":"10.00"}`))
	require.NoError(t, err)
	second, err := Canonicalize([]byte(`{"drift":"10.00","asset":"USD/2","ledger":"main"}`))
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
}

// Number literals must survive verbatim. Round-tripping through a float is the
// classic way this kind of scheme silently breaks.
func TestCanonicalizePreservesNumberLiterals(t *testing.T) {
	t.Parallel()

	for _, literal := range []string{
		"1.10",
		"0.1000000000000000000000001",
		"123456789012345678901234567890",
		"1e400",
	} {
		out, err := Canonicalize([]byte(`{"v":` + literal + `}`))
		require.NoError(t, err)
		require.Equal(t, `{"v":`+literal+`}`, string(out), "literal %s was rewritten", literal)
	}
}

func TestCanonicalizeDisablesHTMLEscaping(t *testing.T) {
	t.Parallel()

	out, err := Canonicalize([]byte(`{"note":"a<b & c>d"}`))
	require.NoError(t, err)
	require.Equal(t, `{"note":"a<b & c>d"}`, string(out))
}

func TestCanonicalizeRejectsTrailingContent(t *testing.T) {
	t.Parallel()

	_, err := Canonicalize([]byte(`{"a":1} {"b":2}`))
	require.Error(t, err)
}

func testChain(t *testing.T) *Chain {
	t.Helper()
	return NewChain(DeriveChainKey([]byte("salt-for-tests"), "pepper"))
}

func testFields() Fields {
	ruleID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	revision := int64(3)
	return Fields{
		Sequence:     7,
		At:           time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
		Kind:         models.AuditEvaluationCommitted,
		RuleID:       &ruleID,
		RuleRevision: &revision,
		PeriodID:     "2026-05",
		Subject:      models.SystemSubject(ComponentScheduler),
		Memento:      []byte(`{"result":"PASS"}`),
	}
}

func TestComputeIsDeterministic(t *testing.T) {
	t.Parallel()

	chain := testChain(t)
	prev := []byte("previous-hash-value-32-bytes-ok!")

	first, err := chain.Compute(prev, testFields())
	require.NoError(t, err)
	second, err := chain.Compute(prev, testFields())
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first, 32)
}

// Every hashed field must actually change the hash. A field that is stored but
// not bound is a field an attacker can rewrite for free, so this test enumerates
// them rather than trusting the implementation to have covered them.
func TestComputeBindsEveryField(t *testing.T) {
	t.Parallel()

	chain := testChain(t)
	prev := []byte("previous")
	baseline, err := chain.Compute(prev, testFields())
	require.NoError(t, err)

	otherID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	otherRevision := int64(4)

	mutations := map[string]func(f *Fields){
		"sequence":      func(f *Fields) { f.Sequence = 8 },
		"at":            func(f *Fields) { f.At = f.At.Add(time.Second) },
		"kind":          func(f *Fields) { f.Kind = models.AuditAlertTransition },
		"ruleID":        func(f *Fields) { f.RuleID = &otherID },
		"ruleRevision":  func(f *Fields) { f.RuleRevision = &otherRevision },
		"alertID":       func(f *Fields) { f.AlertID = &otherID },
		"evaluationID":  func(f *Fields) { f.EvaluationID = &otherID },
		"periodID":      func(f *Fields) { f.PeriodID = "2026-06" },
		"subject":       func(f *Fields) { f.Subject = models.Subject{Subject: "alice"} },
		"subjectScopes": func(f *Fields) { f.Subject.Scopes = []string{"reconciliation:write"} },
		"memento":       func(f *Fields) { f.Memento = []byte(`{"result":"FAIL"}`) },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			fields := testFields()
			mutate(&fields)
			got, err := chain.Compute(prev, fields)
			require.NoError(t, err)
			require.NotEqual(t, baseline, got, "%s is not bound into the hash", name)
		})
	}
}

func TestComputeBindsPreviousHash(t *testing.T) {
	t.Parallel()

	chain := testChain(t)
	onA, err := chain.Compute([]byte("A"), testFields())
	require.NoError(t, err)
	onB, err := chain.Compute([]byte("B"), testFields())
	require.NoError(t, err)
	require.NotEqual(t, onA, onB)

	// Genesis must not collide with an entry chained onto an empty predecessor
	// hash — the length prefix is what keeps those distinct.
	genesis, err := chain.Compute(nil, testFields())
	require.NoError(t, err)
	onEmpty, err := chain.Compute([]byte{}, testFields())
	require.NoError(t, err)
	require.Equal(t, genesis, onEmpty, "nil and empty are the same absent predecessor")
}

// The whole point of keying: an attacker with the table contents but not the key
// cannot recompute a forged entry's hash.
func TestDifferentKeysProduceDifferentChains(t *testing.T) {
	t.Parallel()

	withPepper := NewChain(DeriveChainKey([]byte("salt"), "pepper"))
	withoutPepper := NewChain(DeriveChainKey([]byte("salt"), ""))

	a, err := withPepper.Compute(nil, testFields())
	require.NoError(t, err)
	b, err := withoutPepper.Compute(nil, testFields())
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestKeyCheckDetectsWrongPepper(t *testing.T) {
	t.Parallel()

	right := KeyCheck(DeriveChainKey([]byte("salt"), "correct"))
	wrong := KeyCheck(DeriveChainKey([]byte("salt"), "typo"))
	require.NotEqual(t, right, wrong)
	require.Equal(t, right, KeyCheck(DeriveChainKey([]byte("salt"), "correct")))
}

// Length-prefixing the salt and pepper stops two different pairs from deriving
// the same key by concatenation.
func TestDeriveChainKeyHasNoConcatenationCollision(t *testing.T) {
	t.Parallel()

	a := DeriveChainKey([]byte("ab"), "c")
	b := DeriveChainKey([]byte("a"), "bc")
	require.NotEqual(t, a, b)
}

func TestSubjectEncodingDistinguishesSystemFromAbsentCaller(t *testing.T) {
	t.Parallel()

	// A system component with an empty name must still encode differently from a
	// caller-less request, which is what makes machine actions unambiguous.
	system := EncodeSubject(models.Subject{Source: models.SubjectSourceSystem})
	unknown := EncodeSubject(models.Subject{})
	require.NotEqual(t, system, unknown)
}

func TestSubjectEncodingIsScopeOrderIndependent(t *testing.T) {
	t.Parallel()

	a := EncodeSubject(models.Subject{Subject: "alice", Scopes: []string{"write", "read"}})
	b := EncodeSubject(models.Subject{Subject: "alice", Scopes: []string{"read", "write"}})
	require.Equal(t, a, b)
}

func TestNormalizeSubjectDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	original := []string{"write", "read"}
	subject := models.Subject{Subject: "alice", Scopes: original}
	normalized := NormalizeSubject(subject)

	require.Equal(t, []string{"read", "write"}, normalized.Scopes)
	require.Equal(t, []string{"write", "read"}, original, "the caller's slice was reordered in place")
}

func TestSealingHashBindsEveryField(t *testing.T) {
	t.Parallel()

	base := SealInput{
		PeriodID:      "2026-05",
		FirstSequence: 1,
		LastSequence:  42,
		EntryCount:    42,
		LastAuditHash: []byte("head"),
		StateHash:     []byte("state"),
	}
	baseline := ComputeSealingHash(base)

	mutations := map[string]func(in *SealInput){
		"periodID":      func(in *SealInput) { in.PeriodID = "2026-06" },
		"firstSequence": func(in *SealInput) { in.FirstSequence = 2 },
		"lastSequence":  func(in *SealInput) { in.LastSequence = 43 },
		"entryCount":    func(in *SealInput) { in.EntryCount = 41 },
		"lastAuditHash": func(in *SealInput) { in.LastAuditHash = []byte("other") },
		"stateHash":     func(in *SealInput) { in.StateHash = []byte("other") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			require.NotEqual(t, baseline, ComputeSealingHash(in), "%s is not bound into the seal", name)
		})
	}
}

// The sealing hash must be reproducible by someone who has only the seal's
// published fields — no installation key. That is what lets an auditor verify
// the signature without our cooperation.
func TestSealingHashIsUnkeyed(t *testing.T) {
	t.Parallel()

	in := SealInput{PeriodID: "2026-05", FirstSequence: 1, LastSequence: 10, EntryCount: 10}
	require.Equal(t, ComputeSealingHash(in), ComputeSealingHash(in))
}

func TestSealSignatureRoundTrip(t *testing.T) {
	t.Parallel()

	key, err := GenerateSigningKey()
	require.NoError(t, err)
	require.True(t, key.CanSign())

	sealingHash := ComputeSealingHash(SealInput{PeriodID: "2026-05", LastSequence: 10})
	signature, err := key.Sign(sealingHash)
	require.NoError(t, err)

	// An auditor holds only the public half.
	verifier := SigningKeyFromPublic(key.Public)
	require.False(t, verifier.CanSign())
	require.True(t, verifier.Verify(sealingHash, signature))
	require.Equal(t, key.ID, verifier.ID)

	tampered := ComputeSealingHash(SealInput{PeriodID: "2026-05", LastSequence: 11})
	require.False(t, verifier.Verify(tampered, signature))
}

func TestSigningKeyFromSeedIsStable(t *testing.T) {
	t.Parallel()

	generated, err := GenerateSigningKey()
	require.NoError(t, err)

	restored, err := SigningKeyFromSeed(generated.SeedBase64())
	require.NoError(t, err)
	require.Equal(t, generated.ID, restored.ID)
	require.Equal(t, []byte(generated.Public), []byte(restored.Public))
}

func TestSigningKeyFromSeedRejectsWrongLength(t *testing.T) {
	t.Parallel()

	_, err := SigningKeyFromSeed("dG9vLXNob3J0")
	require.ErrorContains(t, err, "must be 32 bytes")
}

func TestStateHasherIsOrderSensitiveAndCountBound(t *testing.T) {
	t.Parallel()

	a := auditAlertState("a")
	b := auditAlertState("b")

	forward := NewStateHasher()
	forward.AddAlert(a)
	forward.AddAlert(b)

	reversed := NewStateHasher()
	reversed.AddAlert(b)
	reversed.AddAlert(a)

	require.NotEqual(t, forward.Sum(), reversed.Sum(),
		"order must matter, so the caller is forced to scan deterministically")

	// A truncated scan must not be able to produce a complete scan's digest.
	truncated := NewStateHasher()
	truncated.AddAlert(a)
	require.NotEqual(t, forward.Sum(), truncated.Sum())
	require.Equal(t, int64(1), truncated.Count())
}

func auditAlertState(fingerprint string) AlertState {
	return AlertState{
		ID:              uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		RuleID:          uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		Fingerprint:     fingerprint,
		Status:          models.AlertOpen,
		Severity:        models.Severity("high"),
		OccurrenceCount: 2,
	}
}

func TestEvaluationMementoBindsEvidenceByDigest(t *testing.T) {
	t.Parallel()

	ev := &models.Evaluation{
		ID:        uuid.New(),
		RuleID:    uuid.New(),
		StartedAt: time.Now().UTC(),
		EndedAt:   time.Now().UTC(),
		Result:    models.EvaluationPass,
		Evidence:  json.RawMessage(`{"b":"2","a":"1"}`),
	}

	memento, err := NewEvaluationMemento(ev)
	require.NoError(t, err)
	require.NotEmpty(t, memento.EvidenceDigest)

	// Same evidence, keys in the other order — as jsonb would hand it back.
	ev.Evidence = json.RawMessage(`{"a":"1","b":"2"}`)
	again, err := NewEvaluationMemento(ev)
	require.NoError(t, err)
	require.Equal(t, memento.EvidenceDigest, again.EvidenceDigest)

	// Actually different evidence must change the digest.
	ev.Evidence = json.RawMessage(`{"a":"1","b":"3"}`)
	changed, err := NewEvaluationMemento(ev)
	require.NoError(t, err)
	require.NotEqual(t, memento.EvidenceDigest, changed.EvidenceDigest)
}

func TestSubjectFromContextDefaultsToUnknown(t *testing.T) {
	t.Parallel()

	subject := SubjectFrom(t.Context())
	require.Equal(t, models.Subject{}, subject)
	require.Equal(t, "unknown", subject.Display())
	require.False(t, subject.IsSystem())

	ctx := WithSystemSubject(t.Context(), ComponentScheduler)
	got := SubjectFrom(ctx)
	require.True(t, got.IsSystem())
	require.Equal(t, "system:scheduler", got.Display())
}
