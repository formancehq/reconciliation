package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/reconciliation/internal/ledger"
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
