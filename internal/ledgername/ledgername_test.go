package ledgername

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNameContract(t *testing.T) {
	for _, name := range []string{"main", "tenant:prod.eu-west-1", ".ledger", "ledger.", "a..b", "...", "_", "_system", "_tenant", "AZaz09_-:", strings.Repeat("a", 64)} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, Validate(name))
			segment, err := PathSegment(name)
			require.NoError(t, err)
			require.Equal(t, name, segment)
		})
	}
	for _, name := range []string{"", ".", "..", "../main", "main/child", `main\child`, "main?query", "main#fragment", "%2e%2e", "main%2Fchild", "main%252Fchild", "main%00", "ma in", "main\n", "main\x00", "tênant", strings.Repeat("a", 65)} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, Validate(name), ErrInvalid)
			segment, err := PathSegment(name)
			require.ErrorIs(t, err, ErrInvalid)
			require.Empty(t, segment)
		})
	}
}
