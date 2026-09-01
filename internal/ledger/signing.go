package ledger

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// SigningKey is the Ed25519 key pair reconciliation uses to sign every write it
// commits to its control-ledger. The ledger stores the signature natively on the
// committed log entry (see servicepb/signaturepb SignedApplyBatch), so a third
// party holding only the public key can verify — without trusting reconciliation
// or the ledger operator — that a given `_recon` entry was produced by the holder
// of this private key and has not been altered since.
//
// Asymmetric on purpose. This is the external-auditor guarantee at the heart of
// the audit-chain work (EN-1930): the auditor is handed PublicKeyBase64() out of
// band and runs, for any log entry's signature,
//
//	ed25519.Verify(pub, sig.Payload, sig.Signature)
//
// where sig.Payload is the exact serialized ApplyBatch. That reproduces the
// batch bytes and checks the signature with nobody from Formance in the loop. It
// is deliberately not the ledger's HMAC receipt scheme, which is symmetric and
// therefore only self-checkable.
//
// This mirrors internal/audit.SigningKey from the Postgres audit-chain design;
// here the signature travels natively on the ledger log instead of on a period
// seal, so no per-period sealing is required for the guarantee to hold.
type SigningKey struct {
	// ID is a short fingerprint of the public key, sent as SignedApplyBatch.key_id
	// so the ledger — and later a verifier after a rotation — knows which
	// registered key to use. Any stable unique string is accepted by the ledger;
	// a public-key digest keeps it deterministic and collision-free in practice.
	ID      string
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// GenerateSigningKey creates a fresh key pair. The seed can be exported once via
// SeedBase64 and thereafter supplied by --audit-signing-key-seed, so the same
// key — and therefore the same verifiable chain — survives restarts.
func GenerateSigningKey() (SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SigningKey{}, fmt.Errorf("ledger: generating signing key: %w", err)
	}
	return SigningKey{ID: SigningKeyID(pub), Public: pub, Private: priv}, nil
}

// SigningKeyFromSeed rebuilds a key pair from a 32-byte seed supplied out of band
// (flag or secret manager), so the private key never has to be generated or
// persisted by the service. Accepts base64 (standard or URL, padded or not) and
// hex.
func SigningKeyFromSeed(encoded string) (SigningKey, error) {
	seed, err := decodeKeyMaterial(encoded, ed25519.SeedSize)
	if err != nil {
		return SigningKey{}, fmt.Errorf("ledger: decoding signing key seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return SigningKey{}, fmt.Errorf("ledger: signing key seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub, _ := priv.Public().(ed25519.PublicKey)
	return SigningKey{ID: SigningKeyID(pub), Public: pub, Private: priv}, nil
}

// SigningKeyFromPublic builds a verify-only key — what an auditor holds.
func SigningKeyFromPublic(pub ed25519.PublicKey) SigningKey {
	return SigningKey{ID: SigningKeyID(pub), Public: pub}
}

// SigningKeyID is the first 16 hex characters of the public key's SHA-256 digest
// — short enough to quote in a report, long enough not to collide in practice.
// It is the key_id the ledger indexes registered keys by.
func SigningKeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// CanSign reports whether this key holds the private half.
func (k SigningKey) CanSign() bool { return len(k.Private) == ed25519.PrivateKeySize }

// Sign signs the exact serialized ApplyBatch bytes.
func (k SigningKey) Sign(payload []byte) ([]byte, error) {
	if !k.CanSign() {
		return nil, fmt.Errorf("ledger: signing key %q holds no private key", k.ID)
	}
	return ed25519.Sign(k.Private, payload), nil
}

// Verify checks a signature over serialized ApplyBatch bytes. This is exactly
// the check an external auditor performs against the public key.
func (k SigningKey) Verify(payload, signature []byte) bool {
	if len(k.Public) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(k.Public, payload, signature)
}

// PublicKeyBase64 renders the public key for publication — the value an auditor
// is given, and the only value they need.
func (k SigningKey) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(k.Public)
}

// SeedBase64 renders the private seed so a generated key can be exported once
// and thereafter supplied by configuration. Empty for a verify-only key.
func (k SigningKey) SeedBase64() string {
	if !k.CanSign() {
		return ""
	}
	return base64.StdEncoding.EncodeToString(k.Private.Seed())
}

// decodeKeyMaterial accepts the common encodings an operator might paste: base64
// in any of its four flavours, or hex. A hex string is frequently also valid
// base64 (and vice-versa), so it prefers whichever decoding yields exactly
// wantLen bytes; failing that it returns the first successful decode, letting the
// caller's own length check report a useful error.
func decodeKeyMaterial(encoded string, wantLen int) ([]byte, error) {
	decoders := []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
		hex.DecodeString,
	}

	var first []byte
	for _, decode := range decoders {
		b, err := decode(encoded)
		if err != nil {
			continue
		}
		if len(b) == wantLen {
			return b, nil
		}
		if first == nil {
			first = b
		}
	}

	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("value is neither valid base64 nor hex")
}
