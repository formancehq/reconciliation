package ledgerauth

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenProvider_Token(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	tp, err := NewTokenProvider("test-key", priv, "reconciliation")
	require.NoError(t, err)

	token, err := tp.Token()
	require.NoError(t, err)

	// Parse and verify the token.
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	require.NoError(t, err)

	var claims jwt.Claims
	require.NoError(t, parsed.Claims(pub, &claims))

	assert.Equal(t, "reconciliation", claims.Subject)
	assert.NotNil(t, claims.IssuedAt)
	assert.NotNil(t, claims.Expiry)
	assert.True(t, claims.Expiry.Time().After(claims.IssuedAt.Time()))

	// Verify kid header.
	require.Len(t, parsed.Headers, 1)
	assert.Equal(t, "test-key", parsed.Headers[0].KeyID)
	assert.Equal(t, "JWT", parsed.Headers[0].ExtraHeaders["typ"])
}

func TestTokenProvider_TokenIncludesGodClaim(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	tp, err := NewTokenProvider("k1", priv, "sub")
	require.NoError(t, err)

	token, err := tp.Token()
	require.NoError(t, err)

	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, parsed.Claims(pub, &raw))

	god, ok := raw["god"]
	require.True(t, ok, "god claim should be present in every token")
	assert.Equal(t, true, god)
}

func TestTokenProvider_GetRequestMetadata(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	tp, err := NewTokenProvider("k1", priv, "sub1")
	require.NoError(t, err)

	md, err := tp.GetRequestMetadata(context.Background())
	require.NoError(t, err)

	authHeader, ok := md["authorization"]
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(authHeader, "Bearer "))
}

func TestTokenProvider_RequireTransportSecurity(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	tp, err := NewTokenProvider("k1", priv, "sub1")
	require.NoError(t, err)
	assert.False(t, tp.RequireTransportSecurity())
}

func TestTokenProvider_TokenExpiry(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	tp, err := NewTokenProvider("k1", priv, "sub")
	require.NoError(t, err)

	token, err := tp.Token()
	require.NoError(t, err)

	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	require.NoError(t, err)

	var claims jwt.Claims
	require.NoError(t, parsed.Claims(pub, &claims))

	// Token should expire in ~5 minutes.
	expiry := claims.Expiry.Time().Sub(claims.IssuedAt.Time())
	assert.Equal(t, 5*time.Minute, expiry)
}

func TestNewTokenProviderFromFile_RawSeed(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "key.bin")
	require.NoError(t, os.WriteFile(path, priv.Seed(), 0o600))

	tp, err := NewTokenProviderFromFile("raw-key", path, "sub")
	require.NoError(t, err)

	// Verify the token is valid with the expected public key.
	token, err := tp.Token()
	require.NoError(t, err)

	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	require.NoError(t, err)

	var claims jwt.Claims
	require.NoError(t, parsed.Claims(pub, &claims))
	assert.Equal(t, "sub", claims.Subject)
}

func TestNewTokenProviderFromFile_HexSeed(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	hexSeed := hex.EncodeToString(priv.Seed()) + "\n"
	path := filepath.Join(t.TempDir(), "key.hex")
	require.NoError(t, os.WriteFile(path, []byte(hexSeed), 0o600))

	tp, err := NewTokenProviderFromFile("hex-key", path, "sub")
	require.NoError(t, err)

	token, err := tp.Token()
	require.NoError(t, err)

	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	require.NoError(t, err)

	var claims jwt.Claims
	require.NoError(t, parsed.Claims(pub, &claims))
	assert.Equal(t, "sub", claims.Subject)
}

func TestNewTokenProviderFromFile_InvalidSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.bin")
	require.NoError(t, os.WriteFile(path, []byte("too short"), 0o600))

	_, err := NewTokenProviderFromFile("bad", path, "sub")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key file must be")
}
