package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A cursor is client-supplied, so its decoded page size is untrusted: 0 would
// disable the SQL LIMIT entirely and anything above MaxPageSize would bypass the
// cap getPageSize enforces on the query parameter (main: #73). Every handler
// that decodes a cursor validates it.
func TestValidateCursorPageSize(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		pageSize uint64
		wantErr  bool
	}{
		"zero disables the limit": {0, true},
		"one":                     {1, false},
		"at the cap":              {MaxPageSize, false},
		"just over the cap":       {MaxPageSize + 1, true},
		"absurd":                  {1 << 40, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := validateCursorPageSize(tc.pageSize)
			if tc.wantErr {
				require.ErrorIs(t, err, ErrInvalidPageSize)
				return
			}
			require.NoError(t, err)
		})
	}
}
