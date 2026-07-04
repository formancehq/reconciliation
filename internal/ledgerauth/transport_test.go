package ledgerauth

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestSeed(t *testing.T) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "signing.seed")
	require.NoError(t, os.WriteFile(path, priv.Seed(), 0o600))

	return path
}

// TestConfig_Build_RefusesInsecureByDefault is the F2 guard: no TLS and no
// explicit opt-in must be rejected, never silently downgraded to plaintext.
func TestConfig_Build_RefusesInsecureByDefault(t *testing.T) {
	_, _, err := Config{Address: "ledger:8888"}.Build()
	require.ErrorIs(t, err, ErrInsecureTransport)
}

// TestConfig_Build_AllowInsecureOptIn: the explicit opt-in yields insecure
// credentials (local/dev).
func TestConfig_Build_AllowInsecureOptIn(t *testing.T) {
	creds, dialOpts, err := Config{Address: "ledger:8888", AllowInsecure: true}.Build()
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "insecure", creds.Info().SecurityProtocol)
	assert.Empty(t, dialOpts)
}

// TestConfig_Build_TLSNeedsNoOptIn: any TLS material satisfies the guard
// without AllowInsecure.
func TestConfig_Build_TLSNeedsNoOptIn(t *testing.T) {
	caPath, _, _ := writeTestCA(t)

	creds, dialOpts, err := Config{
		Address: "ledger:8888",
		TLS:     TLSConfig{CAFile: caPath, ServerName: "ledger"},
	}.Build()
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "tls", creds.Info().SecurityProtocol)
	assert.Empty(t, dialOpts)
}

// TestConfig_Build_TLSError surfaces a bad TLS config as an error, not a
// silent insecure fallback.
func TestConfig_Build_TLSError(t *testing.T) {
	_, _, err := Config{
		Address: "ledger:8888",
		TLS:     TLSConfig{CAFile: "/does/not/exist"},
	}.Build()
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInsecureTransport)
	assert.Contains(t, err.Error(), "build ledger TLS config")
}

// TestConfig_Build_WithSigning attaches Ed25519 per-RPC signing when a key is
// configured (over TLS).
func TestConfig_Build_WithSigning(t *testing.T) {
	caPath, _, _ := writeTestCA(t)
	seedPath := writeTestSeed(t)

	creds, dialOpts, err := Config{
		Address:     "ledger:8888",
		TLS:         TLSConfig{CAFile: caPath, ServerName: "ledger"},
		AuthKeyID:   "k1",
		AuthKeyFile: seedPath,
		AuthSubject: "reconciliation",
	}.Build()
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Len(t, dialOpts, 1, "one WithPerRPCCredentials dial option expected")
}

// TestConfig_Build_SigningOverInsecure: signing is allowed over an explicitly
// opted-in insecure transport (the TokenProvider does not require TLS).
func TestConfig_Build_SigningOverInsecure(t *testing.T) {
	seedPath := writeTestSeed(t)

	creds, dialOpts, err := Config{
		Address:       "ledger:8888",
		AllowInsecure: true,
		AuthKeyID:     "k1",
		AuthKeyFile:   seedPath,
	}.Build()
	require.NoError(t, err)
	assert.Equal(t, "insecure", creds.Info().SecurityProtocol)
	assert.Len(t, dialOpts, 1)
}

func TestConfig_Build_SigningKeyIDWithoutFile(t *testing.T) {
	_, _, err := Config{AllowInsecure: true, AuthKeyID: "k1"}.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both key-id and key-file")
}

func TestConfig_Build_SigningFileWithoutKeyID(t *testing.T) {
	seedPath := writeTestSeed(t)

	_, _, err := Config{AllowInsecure: true, AuthKeyFile: seedPath}.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both key-id and key-file")
}

// TestConfig_Build_BadSigningKey surfaces a malformed key file as an error.
func TestConfig_Build_BadSigningKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.seed")
	require.NoError(t, os.WriteFile(path, []byte("too short"), 0o600))

	_, _, err := Config{AllowInsecure: true, AuthKeyID: "k1", AuthKeyFile: path}.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load ledger auth key")
}
