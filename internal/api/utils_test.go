package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetQueryBuilderBodyLimit(t *testing.T) {
	t.Parallel()

	t.Run("body within limit", func(t *testing.T) {
		t.Parallel()

		body := `{"$match": {"id": "foo"}}`
		req := httptest.NewRequest(http.MethodGet, "/reconciliations", strings.NewReader(body))

		qb, err := getQueryBuilder(req)
		require.NoError(t, err)
		require.NotNil(t, qb)
	})

	t.Run("oversized body is rejected", func(t *testing.T) {
		t.Parallel()

		body := bytes.Repeat([]byte("a"), maxQueryBuilderBodySize+1)
		req := httptest.NewRequest(http.MethodGet, "/reconciliations", bytes.NewReader(body))

		_, err := getQueryBuilder(req)
		require.Error(t, err)

		var maxBytesErr *http.MaxBytesError
		require.ErrorAs(t, err, &maxBytesErr)
	})
}
