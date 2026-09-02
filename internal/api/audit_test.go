package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/reconciliation/internal/ledger"
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
}
