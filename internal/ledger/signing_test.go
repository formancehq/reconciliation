package ledger

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSigningKeyRoundTrip(t *testing.T) {
	t.Parallel()

	key, err := GenerateSigningKey()
	require.NoError(t, err)
	assert.True(t, key.CanSign())
	assert.Len(t, key.ID, 16, "key id is 16 hex chars")

	msg := []byte("some serialized apply batch")
	sig, err := key.Sign(msg)
	require.NoError(t, err)
	assert.True(t, key.Verify(msg, sig))

	// A verify-only key (what an auditor holds) verifies but cannot sign.
	pub := SigningKeyFromPublic(key.Public)
	assert.False(t, pub.CanSign())
	assert.True(t, pub.Verify(msg, sig))
	assert.Equal(t, key.ID, pub.ID, "id derives from the public key alone")
	_, err = pub.Sign(msg)
	assert.Error(t, err)
}

func TestSigningKeyFromSeedIsDeterministic(t *testing.T) {
	t.Parallel()

	gen, err := GenerateSigningKey()
	require.NoError(t, err)

	// The exported seed rebuilds the exact same key — the operator-pinning path.
	rebuilt, err := SigningKeyFromSeed(gen.SeedBase64())
	require.NoError(t, err)
	assert.Equal(t, gen.ID, rebuilt.ID)
	assert.Equal(t, gen.PublicKeyBase64(), rebuilt.PublicKeyBase64())
	assert.Equal(t, ed25519.PrivateKey(gen.Private), rebuilt.Private)
}

func TestSigningKeyFromSeedAcceptsEncodings(t *testing.T) {
	t.Parallel()

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	want := ed25519.NewKeyFromSeed(seed)

	for name, encoded := range map[string]string{
		"std base64": base64.StdEncoding.EncodeToString(seed),
		"raw base64": base64.RawStdEncoding.EncodeToString(seed),
		"url base64": base64.URLEncoding.EncodeToString(seed),
		"hex":        hex.EncodeToString(seed),
	} {
		t.Run(name, func(t *testing.T) {
			key, err := SigningKeyFromSeed(encoded)
			require.NoError(t, err)
			assert.Equal(t, ed25519.PrivateKey(want), key.Private)
		})
	}
}

func TestSigningKeyFromSeedRejectsWrongLength(t *testing.T) {
	t.Parallel()

	_, err := SigningKeyFromSeed(hex.EncodeToString([]byte("too short")))
	assert.ErrorContains(t, err, "must be")

	_, err = SigningKeyFromSeed("not base64 or hex !!")
	assert.Error(t, err)
}

// TestExternalAuditorFlow is the guarantee the whole feature exists to provide:
// a signed batch is verifiable by a third party using nothing but the public
// key — exactly as they would after reading it back off a ledger log entry.
func TestExternalAuditorFlow(t *testing.T) {
	t.Parallel()

	key, err := GenerateSigningKey()
	require.NoError(t, err)

	// Build and sign a real batch the way applyIdempotent does.
	batch := &servicepb.ApplyBatch{
		IdempotencyKey: "capture:rule-42:2026-09-01",
		Requests: []*servicepb.Request{{
			Type: &servicepb.Request_RegisterSigningKey{
				RegisterSigningKey: &servicepb.RegisterSigningKeyRequest{KeyId: key.ID, PublicKey: key.Public},
			},
		}},
	}
	payload, err := batch.MarshalVT()
	require.NoError(t, err)
	sig, err := key.Sign(payload)
	require.NoError(t, err)

	// The auditor holds only the published public key (base64) and the entry's
	// {payload, signature}. They verify with the stdlib — no Formance code needed.
	pubBytes, err := base64.StdEncoding.DecodeString(key.PublicKeyBase64())
	require.NoError(t, err)
	assert.True(t, ed25519.Verify(pubBytes, payload, sig), "genuine batch verifies")

	// Tampering with the committed bytes breaks the signature.
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-1] ^= 0x01
	assert.False(t, ed25519.Verify(pubBytes, tampered, sig), "altered payload fails")

	// A different key cannot forge a signature this key would accept.
	other, err := GenerateSigningKey()
	require.NoError(t, err)
	forged, err := other.Sign(payload)
	require.NoError(t, err)
	assert.False(t, key.Verify(payload, forged), "signature from another key is rejected")
}
