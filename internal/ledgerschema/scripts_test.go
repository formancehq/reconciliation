package ledgerschema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumscripts(t *testing.T) {
	t.Parallel()

	byName := map[string]NumscriptDef{}
	for _, ns := range Numscripts() {
		require.Equal(t, NumscriptVersion, ns.Version, "%s pinned version", ns.Name)
		require.NotEmpty(t, ns.Content)
		byName[ns.Name] = ns
	}

	require.Len(t, byName, 3)
	require.Contains(t, byName, NumscriptAlertOpen)
	require.Contains(t, byName, NumscriptAlertBump)
	require.Contains(t, byName, NumscriptAlertReopen)
}

func TestNumscriptAlertOpen(t *testing.T) {
	t.Parallel()

	c := content(t, NumscriptAlertOpen)

	// Mints the marker into st:open and the first OCC, both from the pool via
	// unbounded overdraft.
	require.Contains(t, c, "["+AssetAlert+" 1]")
	require.Contains(t, c, "["+AssetOcc+" 1]")
	require.Contains(t, c, "source = $"+VarPool+" allowing unbounded overdraft")
	require.Contains(t, c, "destination = $"+VarStOpen)
	require.Contains(t, c, "destination = $"+VarItem)
	requireDeclaresVars(t, c, VarPool, VarStOpen, VarItem)
}

func TestNumscriptAlertBump(t *testing.T) {
	t.Parallel()

	c := content(t, NumscriptAlertBump)

	// A repeat mints only OCC — no ALERT marker move.
	require.Contains(t, c, "["+AssetOcc+" 1]")
	require.NotContains(t, c, "["+AssetAlert+" 1]")
	require.Contains(t, c, "source = $"+VarPool+" allowing unbounded overdraft")
	requireDeclaresVars(t, c, VarPool, VarItem)
}

func TestNumscriptAlertReopen(t *testing.T) {
	t.Parallel()

	c := content(t, NumscriptAlertReopen)

	// Guarded marker move st_from → st_open: the source is bare (no overdraft),
	// so an absent marker fails the batch (compare-and-swap). Plus an OCC bump.
	require.Contains(t, c, "source = $"+VarStFrom+"\n")
	require.NotContains(t, c, "$"+VarStFrom+" allowing unbounded overdraft")
	require.Contains(t, c, "destination = $"+VarStOpen)
	require.Contains(t, c, "["+AssetOcc+" 1]")
	requireDeclaresVars(t, c, VarPool, VarStFrom, VarStOpen, VarItem)
}

func content(t *testing.T, name string) string {
	t.Helper()

	for _, ns := range Numscripts() {
		if ns.Name == name {
			return ns.Content
		}
	}

	t.Fatalf("numscript %q not found", name)

	return ""
}

// requireDeclaresVars asserts every var is declared in the `vars { ... }` block.
func requireDeclaresVars(t *testing.T, c string, vars ...string) {
	t.Helper()

	for _, v := range vars {
		require.Contains(t, c, "account $"+v, "vars block must declare $%s", v)
	}

	require.True(t, strings.HasPrefix(c, "vars {"), "script must open with a vars block")
}
