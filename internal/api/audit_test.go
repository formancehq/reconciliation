package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListSigningKeysHandler(t *testing.T) {
	t.Parallel()

	t.Run("serves registered keys with base64 public keys", func(t *testing.T) {
		t.Parallel()
		fake := &fakeIntrospector{signingKeys: []ledger.SigningKeyInfo{
			{KeyID: "6d00a939e0c68f7a", PublicKey: []byte{0x01, 0x02, 0x03}},
			{KeyID: "child", PublicKey: []byte{0x04}, ParentKeyID: "6d00a939e0c68f7a"},
		}}
		rec := httptest.NewRecorder()
		listSigningKeysHandler(fake).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/signing-keys", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[signingKeysResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Len(t, got.Data.Keys, 2)
		require.Equal(t, "6d00a939e0c68f7a", got.Data.Keys[0].KeyID)
		require.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}), got.Data.Keys[0].PublicKey)
		require.Equal(t, "6d00a939e0c68f7a", got.Data.Keys[1].ParentKeyID)
	})

	t.Run("best-effort: a read error yields an empty set, not a failure", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		listSigningKeysHandler(&fakeIntrospector{signingErr: errors.New("ledger down")}).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/signing-keys", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[signingKeysResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Empty(t, got.Data.Keys)
	})
}

// firstEntryField returns the raw JSON of one field of the first served entry.
func firstEntryField(t *testing.T, body []byte, field string) string {
	t.Helper()

	var got struct {
		Data struct {
			Entries []map[string]json.RawMessage `json:"entries"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.NotEmpty(t, got.Data.Entries)

	return string(got.Data.Entries[0][field])
}

func TestListAuditEntriesHandler(t *testing.T) {
	t.Parallel()

	t.Run("serves entries with base64 payload/signature and the sequence", func(t *testing.T) {
		t.Parallel()
		ts := time.Date(2026, 9, 2, 13, 2, 46, 0, time.UTC)
		fake := &fakeIntrospector{auditItems: []ledger.AuditEntryInfo{
			{Sequence: 286, Timestamp: ts, KeyID: "6d00a939e0c68f7a", Payload: []byte{0x0a, 0x0b}, Signature: []byte{0x0c}, Signed: true, Outcome: "success", OrderCount: 1, Ledgers: []string{"reconciliation"}},
			{Sequence: 42, Outcome: "success", Signed: false}, // unsigned bootstrap write
		}}
		rec := httptest.NewRecorder()
		listAuditEntriesHandler(fake, ControlLedger("reconciliation")).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/entries", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[auditEntriesResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Len(t, got.Data.Entries, 2)

		first := got.Data.Entries[0]
		assert.Equal(t, uint64(286), first.Sequence)
		assert.Equal(t, "6d00a939e0c68f7a", first.KeyID)
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x0a, 0x0b}), first.Payload)
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x0c}), first.Signature)
		assert.True(t, first.Signed)
		assert.Equal(t, "2026-09-02T13:02:46Z", first.Timestamp)

		assert.False(t, got.Data.Entries[1].Signed)
		assert.Empty(t, got.Data.Entries[1].Payload)
		assert.Equal(t, defaultAuditEntriesLimit, fake.auditLimit)
	})

	t.Run("clamps limit to the maximum", func(t *testing.T) {
		t.Parallel()
		fake := &fakeIntrospector{}
		rec := httptest.NewRecorder()
		listAuditEntriesHandler(fake, "reconciliation").
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/entries?limit=99999", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, maxAuditEntriesLimit, fake.auditLimit)
	})

	t.Run("best-effort: a read error yields an empty set, not a failure", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		listAuditEntriesHandler(&fakeIntrospector{auditErr: errors.New("ledger down")}, "reconciliation").
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/entries", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[auditEntriesResponse]
		sharedapi.Decode(t, rec.Body, &got)
		require.Empty(t, got.Data.Entries)
	})

	t.Run("serves the action a write records and the actions it requested", func(t *testing.T) {
		t.Parallel()
		at := time.Date(2026, 10, 2, 13, 30, 0, 0, time.UTC)
		fake := &fakeIntrospector{auditItems: []ledger.AuditEntryInfo{{
			Sequence: 325,
			Outcome:  "success",
			Actions:  []ledger.AuditAction{{Kind: "Create transaction", Ledger: "reconciliation", Detail: "alert_move v2.0.0"}},
			Activity: &ledger.AuditActivity{Kind: "alert.acknowledged", RuleID: "r-1", ContractVersion: 2, OccurredAt: at, Payload: []byte(`{"newStatus":"ACKNOWLEDGED"}`)},
		}}}
		rec := httptest.NewRecorder()
		listAuditEntriesHandler(fake, "reconciliation").
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/entries", nil))

		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{
			"kind": "alert.acknowledged",
			"ruleId": "r-1",
			"contractVersion": 2,
			"occurredAt": "2026-10-02T13:30:00Z",
			"payload": {"newStatus": "ACKNOWLEDGED"}
		}`, firstEntryField(t, rec.Body.Bytes(), "activity"))
		assert.JSONEq(t, `[{"kind": "Create transaction", "ledger": "reconciliation", "detail": "alert_move v2.0.0"}]`, firstEntryField(t, rec.Body.Bytes(), "actions"))
	})

	t.Run("lists reconciliation actions by default", func(t *testing.T) {
		t.Parallel()
		fake := &fakeIntrospector{}
		listAuditEntriesHandler(fake, "reconciliation").
			ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/audit/entries", nil))

		assert.Equal(t, ledger.AuditScopeActions, fake.auditScope)
	})

	t.Run("passes the requested scope", func(t *testing.T) {
		t.Parallel()
		for _, scope := range []ledger.AuditScope{ledger.AuditScopeActions, ledger.AuditScopeSystem, ledger.AuditScopeAll} {
			fake := &fakeIntrospector{}
			rec := httptest.NewRecorder()
			listAuditEntriesHandler(fake, "reconciliation").
				ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/entries?scope="+string(scope), nil))

			require.Equal(t, http.StatusOK, rec.Code, scope)
			assert.Equal(t, scope, fake.auditScope)
		}
	})

	t.Run("an unknown scope is a validation error", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		listAuditEntriesHandler(&fakeIntrospector{}, "reconciliation").
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit/entries?scope=everything", nil))

		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestGetAuditEntryByTransactionHandler(t *testing.T) {
	t.Parallel()

	serve := func(fake *fakeIntrospector, target string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Get("/audit/entries/by-transaction/{transactionId}",
			getAuditEntryByTransactionHandler(fake, ControlLedger("reconciliation")))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	t.Run("resolves a transaction id to its signed audit entry", func(t *testing.T) {
		t.Parallel()
		// Control-ledger tx 38 -> the audit entry with the bucket-wide sequence 41.
		fake := &fakeIntrospector{resolveByTx: map[uint64]ledger.AuditEntryInfo{
			38: {Sequence: 41, KeyID: "6d00a939e0c68f7a", Payload: []byte{0x0a}, Signature: []byte{0x0c}, Signed: true, Outcome: "success"},
		}}
		rec := serve(fake, "/audit/entries/by-transaction/38")

		require.Equal(t, http.StatusOK, rec.Code)
		var got sharedapi.BaseResponse[auditEntry]
		sharedapi.Decode(t, rec.Body, &got)
		assert.Equal(t, uint64(41), got.Data.Sequence, "returns the audit sequence, not the tx id")
		assert.True(t, got.Data.Signed)
	})

	t.Run("404 when no entry matches the transaction", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{resolveByTx: map[uint64]ledger.AuditEntryInfo{}}, "/audit/entries/by-transaction/999")
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("400 on a non-numeric transaction id", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{}, "/audit/entries/by-transaction/not-a-number")
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("404, never a proof, when the entry's signed payload does not carry the transaction", func(t *testing.T) {
		t.Parallel()
		mismatch := fmt.Errorf("%w: entry 41, transaction 38", ledger.ErrAuditEntryMismatch)
		rec := serve(&fakeIntrospector{auditErr: mismatch}, "/audit/entries/by-transaction/38")
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("404 on a resolver read error (best-effort, no 500)", func(t *testing.T) {
		t.Parallel()
		rec := serve(&fakeIntrospector{auditErr: errors.New("ledger unreachable")}, "/audit/entries/by-transaction/38")
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}
