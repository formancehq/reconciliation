package ledgerschema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumscriptMintMarker(t *testing.T) {
	t.Parallel()

	got := NumscriptMintMarker("alert:issued:rule:R:per:P", "alert:st:open:rule:R:per:P:fp:H")

	require.Equal(t, `send [ALERT 1] (
	source = @alert:issued:rule:R:per:P allowing unbounded overdraft
	destination = @alert:st:open:rule:R:per:P:fp:H
)`, got)
}

func TestNumscriptMoveMarker(t *testing.T) {
	t.Parallel()

	got := NumscriptMoveMarker("alert:st:resolved:rule:R:per:P:fp:H", "alert:st:open:rule:R:per:P:fp:H")

	// The source is a bare account (no overdraft) — the compare-and-swap guard.
	require.Equal(t, `send [ALERT 1] (
	source = @alert:st:resolved:rule:R:per:P:fp:H
	destination = @alert:st:open:rule:R:per:P:fp:H
)`, got)
	require.NotContains(t, got, "allowing unbounded overdraft")
}

func TestNumscriptMintOcc(t *testing.T) {
	t.Parallel()

	got := NumscriptMintOcc("alert:occ:rule:R:per:P", "alert:item:rule:R:per:P:fp:H")

	require.Equal(t, `send [OCC 1] (
	source = @alert:occ:rule:R:per:P allowing unbounded overdraft
	destination = @alert:item:rule:R:per:P:fp:H
)`, got)
}

// TestNumscriptAssetsMatchSchema guards against the asset constants drifting
// away from what the scripts actually emit.
func TestNumscriptAssetsMatchSchema(t *testing.T) {
	t.Parallel()

	require.True(t, strings.Contains(NumscriptMintMarker("a", "b"), "["+AssetAlert+" 1]"))
	require.True(t, strings.Contains(NumscriptMintOcc("a", "b"), "["+AssetOcc+" 1]"))
}
