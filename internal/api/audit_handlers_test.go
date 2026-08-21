package api

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"

	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
)

func auditRouter(t *testing.T) (*httptest.Server, *auditBackend) {
	t.Helper()
	b, svc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, &auditBackend{svc: svc}
}

// auditBackend wraps the generated mock so the filter assertions below read as
// intent rather than as gomock plumbing.
type auditBackend struct {
	svc *backend.MockService
}

// expectList asserts on the filters the handler parsed out of the query string.
// Checking them at this boundary is the point: the handler is the only place that
// turns strings into a query, so a mis-parse here silently answers a different
// question than the caller asked.
func (a *auditBackend) expectList(assert func(storage.AuditEntryFilters, int64, int)) {
	a.svc.EXPECT().
		ListAuditEntries(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, f storage.AuditEntryFilters, after int64, limit int) ([]models.AuditEntry, int64, error) {
			assert(f, after, limit)
			return nil, 0, nil
		})
	a.svc.EXPECT().ChainHead(gomock.Any()).Return(int64(0), nil, nil)
}

func get(t *testing.T, srv *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	res, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	var body map[string]any
	if res.ContentLength != 0 {
		_ = json.NewDecoder(res.Body).Decode(&body)
	}
	return res.StatusCode, body
}

func put(t *testing.T, srv *httptest.Server, path, payload string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, srv.URL+path, strings.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	var body map[string]any
	if res.ContentLength != 0 {
		_ = json.NewDecoder(res.Body).Decode(&body)
	}
	return res.StatusCode, body
}

func post(t *testing.T, srv *httptest.Server, path, payload string) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if payload == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(payload)
	}
	res, err := http.Post(srv.URL+path, "application/json", reader)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	var body map[string]any
	if res.ContentLength != 0 {
		_ = json.NewDecoder(res.Body).Decode(&body)
	}
	return res.StatusCode, body
}

// --- parseAuditFilters ------------------------------------------------------
//
// This is input validation on an authenticated endpoint, so every rejection path
// is worth pinning: a filter that silently ignores a malformed value would answer
// a different question than the caller asked, which in an audit context is worse
// than an error.

func TestListAuditEntries_RejectsBadFilters(t *testing.T) {
	t.Parallel()
	srv, _ := auditRouter(t)

	for _, tc := range []struct{ name, query, wants string }{
		{"unknown kind", "?kind=rule.exploded", "unknown kind"},
		{"malformed ruleID", "?ruleID=not-a-uuid", "invalid ruleID"},
		{"malformed alertID", "?alertID=123", "invalid alertID"},
		{"bad actor", "?actor=robot", "actor must be"},
		{"negative sequence", "?fromSequence=-1", "non-negative"},
		{"unparseable sequence", "?toSequence=abc", "non-negative"},
		{"bad timestamp", "?from=yesterday", "RFC3339"},
		{"pageSize zero", "?pageSize=0", "between 1 and 1000"},
		{"pageSize too large", "?pageSize=5000", "between 1 and 1000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := get(t, srv, "/audit-entries"+tc.query)
			require.Equal(t, http.StatusBadRequest, status)
			require.Equal(t, "VALIDATION", body["errorCode"])
			require.Contains(t, body["errorMessage"], tc.wants)
		})
	}
}

func TestListAuditEntries_AcceptsEveryDocumentedFilter(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	ruleID := uuid.New()
	rec.expectList(func(f storage.AuditEntryFilters, after int64, limit int) {
		require.Equal(t, []models.AuditEntryKind{
			models.AuditEvaluationCommitted, models.AuditAlertTransition,
		}, f.Kinds)
		require.Equal(t, ruleID, *f.RuleID)
		require.Equal(t, "2026-05", f.PeriodID)
		require.True(t, f.SystemOnly)
		require.False(t, f.HumanOnly)
		require.Equal(t, int64(10), f.FromSeq)
		require.Equal(t, int64(90), f.ToSeq)
		require.NotNil(t, f.From)
		require.Equal(t, int64(5), after)
		require.Equal(t, 250, limit)
	})

	status, _ := get(t, srv, "/audit-entries?kind=evaluation.committed,alert.transition"+
		"&ruleID="+ruleID.String()+"&periodID=2026-05&actor=system"+
		"&fromSequence=10&toSequence=90&from=2026-05-01T00:00:00Z&after=5&pageSize=250")
	require.Equal(t, http.StatusOK, status)
}

// "Anything not provably a machine counts as human" is a documented, load-bearing
// choice: an unattributed entry must never be filed as machine-run.
func TestListAuditEntries_ActorHumanIsTheConservativeSide(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	rec.expectList(func(f storage.AuditEntryFilters, _ int64, _ int) {
		require.True(t, f.HumanOnly)
		require.False(t, f.SystemOnly)
	})
	status, _ := get(t, srv, "/audit-entries?actor=human")
	require.Equal(t, http.StatusOK, status)
}

// --- render ----------------------------------------------------------------

func TestGetAuditEntry_ExposesTheHashedBytes(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	memento := []byte(`{"result":"PASS"}`)
	ruleID, alertID := uuid.New(), uuid.New()
	revision := int64(3)
	entry := &models.AuditEntry{
		Sequence: 7, At: time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
		Kind: models.AuditEvaluationCommitted, RuleID: &ruleID, RuleRevision: &revision,
		AlertID: &alertID, PeriodID: "2026-05",
		Subject:       models.SystemSubject("scheduler"),
		Memento:       memento,
		MementoDigest: []byte{0xde, 0xad},
		PrevHash:      []byte{0xbe, 0xef},
		Hash:          []byte{0xca, 0xfe},
		HashVersion:   1,
	}
	rec.svc.EXPECT().GetAuditEntry(gomock.Any(), int64(7)).Return(entry, nil)

	status, body := get(t, srv, "/audit-entries/7")
	require.Equal(t, http.StatusOK, status)
	data := body["data"].(map[string]any)

	// The base64 memento is the authoritative form — it must be the exact bytes,
	// not a re-serialisation of the parsed view, or verification is meaningless.
	require.Equal(t, base64.StdEncoding.EncodeToString(memento), data["memento"])
	require.Equal(t, "PASS", data["mementoJSON"].(map[string]any)["result"])
	require.Equal(t, hex.EncodeToString([]byte{0xca, 0xfe}), data["hash"])
	require.Equal(t, hex.EncodeToString([]byte{0xbe, 0xef}), data["prevHash"])

	subject := data["subject"].(map[string]any)
	require.True(t, subject["system"].(bool))
	require.Equal(t, "system:scheduler", subject["actor"])
}

func TestGetAuditEntry_RejectsNonNumericSequence(t *testing.T) {
	t.Parallel()
	srv, _ := auditRouter(t)

	for _, seq := range []string{"abc", "0", "-3"} {
		status, body := get(t, srv, "/audit-entries/"+seq)
		require.Equal(t, http.StatusBadRequest, status, "sequence %q", seq)
		require.Contains(t, body["errorMessage"], "positive integer")
	}
}

// --- verify ----------------------------------------------------------------

// A detected violation is a successful request with a bad answer. Returning 200
// is deliberate, and a client that checks the status code instead of `ok` would
// read tampering as success — so it is pinned here.
func TestVerifyChain_ViolationIsATwoHundred(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	at := int64(42)
	rec.svc.EXPECT().VerifyChain(gomock.Any(), int64(0), int64(0)).Return(&models.ChainVerification{
		OK: false, Violation: models.ChainViolationHashMismatch, AtSequence: &at,
		Detail: "entry 42's stored hash does not match its contents",
	}, nil)

	status, body := post(t, srv, "/audit-entries/verify", `{}`)
	require.Equal(t, http.StatusOK, status)
	data := body["data"].(map[string]any)
	require.False(t, data["ok"].(bool))
	require.Equal(t, "HASH_MISMATCH", data["violation"])
	require.EqualValues(t, 42, data["atSequence"])
}

func TestVerifyChain_EmptyBodyVerifiesEverything(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	rec.svc.EXPECT().VerifyChain(gomock.Any(), int64(0), int64(0)).
		Return(&models.ChainVerification{OK: true, EntriesWalked: 12}, nil)

	status, body := post(t, srv, "/audit-entries/verify", "")
	require.Equal(t, http.StatusOK, status)
	require.True(t, body["data"].(map[string]any)["ok"].(bool))
}

func TestVerifyChain_PeriodAndRangeAreMutuallyExclusive(t *testing.T) {
	t.Parallel()
	srv, _ := auditRouter(t)

	status, body := post(t, srv, "/audit-entries/verify", `{"periodID":"2026-05","fromSequence":3}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body["errorMessage"], "not both")
}

// A closure that covered nothing stores lastSequence = firstSequence - 1.
// Forwarding that to VerifyChain would read as "no upper bound" and verify the
// whole journal, answering about a different range than the caller asked about.
//
// But empty is not automatically intact: there are no entries to recompute, so a
// walk would cross no closure and re-derive nothing, while the closure itself can
// still have been edited. It is therefore checked directly.
func TestVerifyChain_EmptyClosureChecksTheClosureItself(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	last := int64(0)
	closure := &models.Closure{ID: 4, FirstSequence: 1, LastSequence: &last}
	rec.svc.EXPECT().AttestationsForPeriod(gomock.Any(), "2026-04").
		Return([]storage.PeriodAttestation{{
			Period:  models.ClosurePeriod{PeriodID: "2026-04"},
			Closure: closure,
		}}, nil)
	rec.svc.EXPECT().VerifyClosure(gomock.Any(), int64(4)).Return(closure, true, "", nil)
	// Deliberately no VerifyChain expectation: walking the journal here would be
	// answering the wrong question.

	status, body := post(t, srv, "/audit-entries/verify", `{"periodID":"2026-04"}`)
	require.Equal(t, http.StatusOK, status)
	data := body["data"].(map[string]any)
	require.True(t, data["ok"].(bool))
	require.EqualValues(t, 1, data["firstSequence"])
	require.EqualValues(t, 0, data["lastSequence"])
	require.Equal(t, []any{"2026-04"}, data["sealsCrossed"])
}

// And an edited closure over an empty range must not report intact.
func TestVerifyChain_EmptyClosureReportsAnEditedClosure(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	last := int64(0)
	closure := &models.Closure{ID: 4, FirstSequence: 1, LastSequence: &last}
	rec.svc.EXPECT().AttestationsForPeriod(gomock.Any(), "2026-04").
		Return([]storage.PeriodAttestation{{
			Period:  models.ClosurePeriod{PeriodID: "2026-04"},
			Closure: closure,
		}}, nil)
	rec.svc.EXPECT().VerifyClosure(gomock.Any(), int64(4)).
		Return(closure, false, "closure 4 no longer reproduces its own sealing hash", nil)

	status, body := post(t, srv, "/audit-entries/verify", `{"periodID":"2026-04"}`)
	require.Equal(t, http.StatusOK, status)
	data := body["data"].(map[string]any)
	require.False(t, data["ok"].(bool))
	require.Equal(t, "HASH_MISMATCH", data["violation"])
	require.Contains(t, data["detail"], "no longer reproduces")
}

// --- periods and closures --------------------------------------------------

// A period nothing has attested yet is a legitimate answer, not a missing
// resource: absence IS the open state, and a 404 would leave a client guessing
// whether the period is open or the id was wrong.
func TestGetPeriod_UnattestedPeriodIsNotAFourOhFour(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	rec.svc.EXPECT().AttestationsForPeriod(gomock.Any(), "2026-09").Return(nil, storage.ErrNotFound)

	status, body := get(t, srv, "/periods/2026-09")
	require.Equal(t, http.StatusOK, status)
	data := body["data"].(map[string]any)
	require.Equal(t, "OPEN", data["status"])
	require.Equal(t, "2026-09", data["periodID"])
	require.Empty(t, data["attestations"])
}

// The read path still answers in business periods even though closing no longer
// takes one, and the period's own figures come back alongside the closure that
// attested them.
func TestGetPeriod_RendersTheAttestationAndItsClosure(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	last := int64(40)
	closedAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	rec.svc.EXPECT().AttestationsForPeriod(gomock.Any(), "2026-05").
		Return([]storage.PeriodAttestation{{
			Period: models.ClosurePeriod{
				PeriodID: "2026-05", EntryCount: 12, AlertCount: 5, UnresolvedCount: 2,
				Ended: true, Frozen: true,
			},
			Closure: &models.Closure{
				ID: 7, Status: models.ClosureClosed, FirstSequence: 1, LastSequence: &last,
				EntryCount: 40, ClosedAt: &closedAt,
				SealingHash: []byte{0x01, 0x02}, Signature: []byte{0x03, 0x04}, SigningKeyID: "abc123",
				ClosedBy: models.Subject{Subject: "controller@acme.com", Source: models.SubjectSourceIssuer},
			},
		}}, nil)

	status, body := get(t, srv, "/periods/2026-05")
	require.Equal(t, http.StatusOK, status)
	data := body["data"].(map[string]any)
	require.Equal(t, "SEALED", data["status"])

	attestations := data["attestations"].([]any)
	require.Len(t, attestations, 1)
	first := attestations[0].(map[string]any)
	period := first["period"].(map[string]any)
	require.EqualValues(t, 2, period["unresolvedCount"])
	require.True(t, period["frozen"].(bool))

	closure := first["closure"].(map[string]any)
	require.EqualValues(t, 7, closure["id"])
	require.Equal(t, hex.EncodeToString([]byte{0x01, 0x02}), closure["sealingHash"])
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x03, 0x04}), closure["signature"])
	require.False(t, closure["closedBy"].(map[string]any)["system"].(bool))
}

// Closing takes no argument at all. That absence is the design: a seal derived
// its range from a period id the caller typed, which is what made an ordering
// mistake possible and permanent.
func TestCloseJournal_RendersTheSignedClosure(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	last := int64(40)
	rec.svc.EXPECT().CloseJournal(gomock.Any()).Return(&models.Closure{
		ID: 7, Status: models.ClosureClosed, FirstSequence: 1, LastSequence: &last, EntryCount: 40,
		Periods: []models.ClosurePeriod{
			{PeriodID: "2026-05", AlertCount: 5, UnresolvedCount: 2, Ended: true, Frozen: true},
			{PeriodID: "continuous", AlertCount: 1},
		},
		SealingHash: []byte{0x01, 0x02}, Signature: []byte{0x03, 0x04}, SigningKeyID: "abc123",
	}, nil)

	status, body := post(t, srv, "/closures", "")
	require.Equal(t, http.StatusCreated, status)
	data := body["data"].(map[string]any)
	require.Equal(t, "CLOSED", data["status"])
	require.EqualValues(t, 7, data["id"])

	periods := data["periods"].([]any)
	require.Len(t, periods, 2)
	// continuous never ends, so a closing never freezes it — which is what lets
	// live monitoring run across closings untouched.
	require.False(t, periods[1].(map[string]any)["frozen"].(bool))
}

// A schedule that does not parse is a silent outage: rotation stops and nobody
// finds out until an auditor asks why the books were never closed.
func TestSetClosingSchedule_RejectsAnInvalidCron(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	rec.svc.EXPECT().SetClosingSchedule(gomock.Any(), "not a cron").
		Return(fmt.Errorf("%w: bad cron", service.ErrValidation))

	status, body := put(t, srv, "/closing-schedule", `{"cron":"not a cron"}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "VALIDATION", body["errorCode"])
}

func TestListVerificationKeys_PublishesRetiredKeysToo(t *testing.T) {
	t.Parallel()
	srv, rec := auditRouter(t)

	retired := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rec.svc.EXPECT().ListVerificationKeys(gomock.Any()).Return([]storage.VerificationKey{
		{KeyID: "new", Algorithm: "Ed25519", Active: true, PublicKey: "cHVi"},
		{KeyID: "old", Algorithm: "Ed25519", Active: false, RetiredAt: &retired, PublicKey: "b2xk"},
	}, nil)

	status, body := get(t, srv, "/audit-signing-keys")
	require.Equal(t, http.StatusOK, status)
	keys := body["data"].([]any)
	require.Len(t, keys, 2, "a seal signed years ago must stay checkable after a rotation")
	require.False(t, keys[1].(map[string]any)["active"].(bool))
}
