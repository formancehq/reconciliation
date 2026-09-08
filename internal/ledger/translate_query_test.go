package ledger

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TranslateDataQuery is the seam the API layer runs a stored query through, so
// this pins the dialect rather than the plumbing: the shape a rule's
// source.query is written in, and the shape a stale_holds alert records as
// effectiveQuery. A change here changes what an operator can paste back.
func TestTranslateDataQuery(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		query   string
		wantNil bool
		wantErr string
	}{
		{name: "empty query is no filter", query: "", wantNil: true},
		{name: "empty object is no filter", query: `{}`, wantNil: true},
		{name: "exact address", query: `{"$match":{"address":"holds:h-1"}}`},
		{name: "address prefix", query: `{"$match":{"address":"holds:*"}}`},
		{name: "metadata string", query: `{"$match":{"metadata[desk]":"ops1"}}`},
		{name: "metadata integer bound", query: `{"$lte":{"metadata[hold_expires_at]":1757322900000000}}`},
		{name: "metadata exists", query: `{"$exists":{"metadata[hold_expires_at]":true}}`},
		{name: "and over leaves", query: `{"$and":[{"$match":{"address":"holds:*"}},{"$match":{"metadata[desk]":"ops1"}}]}`},
		{name: "not", query: `{"$not":{"$match":{"address":"holds:h-1"}}}`},
		{
			// The shape stale_holds renders when a rule declares both a recorded
			// expiry and a created-at fallback — the one an operator is most
			// likely to take from an alert.
			name: "the stale_holds fallback tree",
			query: `{"$and":[{"$match":{"address":"holds:*"}},{"$or":[` +
				`{"$and":[{"$exists":{"metadata[hold_expires_at]":true}},{"$lte":{"metadata[hold_expires_at]":1757322900000000}}]},` +
				`{"$and":[{"$exists":{"metadata[hold_expires_at]":false}},{"$lte":{"metadata[hold_created_at]":1757150100000000}}]}]}]}`,
		},
		{
			name:    "malformed JSON names the input",
			query:   `{{{`,
			wantErr: "invalid character",
		},
		{
			name:    "unsupported operator on address",
			query:   `{"$nope":{"address":"x"}}`,
			wantErr: "address supports only $match",
		},
		{
			name:    "unsupported field",
			query:   `{"$match":{"balance":"1"}}`,
			wantErr: "unsupported key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			filter, err := TranslateDataQuery(json.RawMessage(tc.query))
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)

				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				require.Nil(t, filter, "an unconstrained query must leave the caller to decide the default")

				return
			}
			require.NotNil(t, filter)
		})
	}
}
