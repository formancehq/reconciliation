package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/zeebo/blake3"
)

const (
	// chainKeyContext domain-separates the chain key derivation. Changing this
	// string invalidates every existing chain, so it is versioned.
	chainKeyContext = "formance:reconciliation:audit-hash:blake3:v1"
	// keyCheckMessage is MAC'd under the chain key to produce a value that
	// proves possession of the key without revealing it.
	keyCheckMessage = "formance:reconciliation:audit-key-check:v1"
)

// DeriveChainKey produces the installation's chain key from two inputs.
//
// The salt is generated once and persisted in the database. The pepper is
// optional and supplied out-of-band by the operator (flag or environment). The
// distinction matters: with a pepper configured, someone holding only a database
// dump — a stolen backup, a compromised read replica, a DBA — cannot recompute
// hashes for forged entries, because the key material is not all in the
// database. Without one, the chain still detects accidental corruption and
// application-level tampering, but an attacker with full database access and
// knowledge of this code could in principle rebuild the chain.
//
// Ledger V2 had no equivalent: its hash was computed by a database function, so
// database access was sufficient to rewrite the chain. This is a deliberate
// improvement on the precedent, not a copy of it.
func DeriveChainKey(salt []byte, pepper string) [32]byte {
	// Length-prefix both parts: a bare salt||pepper concatenation would let two
	// different (salt, pepper) pairs produce identical material.
	w := &payloadWriter{}
	w.bytesField(salt)
	w.stringField(pepper)

	var out [32]byte
	blake3.DeriveKey(chainKeyContext, w.bytes(), out[:])
	return out
}

// KeyCheck returns a value that can be stored and compared at boot to detect a
// missing or wrong pepper. It is a MAC of a fixed message under the chain key,
// so publishing it does not weaken the key.
//
// A mismatch at boot is fatal by design, mirroring the Ledger's fatal
// cluster-id mismatch: continuing with the wrong key would silently append
// entries that no later verification can reconcile with the ones before them.
func KeyCheck(key [32]byte) []byte {
	h, err := blake3.NewKeyed(key[:])
	if err != nil {
		// NewKeyed only fails on a wrong key length, which is impossible here.
		panic(fmt.Sprintf("audit: keyed hasher for key check: %v", err))
	}
	_, _ = h.Write([]byte(keyCheckMessage))
	out := make([]byte, digestLen)
	_, _ = h.Digest().Read(out)
	return out
}

// NewSalt generates the per-installation chain salt.
func NewSalt() ([]byte, error) {
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("audit: generating chain salt: %w", err)
	}
	return salt, nil
}

// SigningKey is the Ed25519 key pair whose signatures make a period seal
// verifiable by a third party.
//
// Asymmetric on purpose. The chain hashes are keyed, so nobody outside this
// installation can check them — which would leave a client's auditor trusting
// our own verification endpoint, precisely the "black box" objection this
// feature exists to answer. A signed seal closes that: the auditor holds the
// public key, recomputes the sealing hash from the seal's own published fields,
// and checks the signature without us being involved.
//
// This is deliberately not the Ledger's receipt scheme, which is HMAC-SHA256
// and symmetric — its own documentation notes that no external auditor needs to
// verify a receipt. Here that is the entire point.
type SigningKey struct {
	// ID is a short fingerprint of the public key, carried on each seal so a
	// verifier knows which key to use after a rotation.
	ID      string
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// GenerateSigningKey creates a fresh key pair.
func GenerateSigningKey() (SigningKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SigningKey{}, fmt.Errorf("audit: generating signing key: %w", err)
	}
	return SigningKey{ID: SigningKeyID(pub), Public: pub, Private: priv}, nil
}

// SigningKeyFromSeed rebuilds a key pair from a 32-byte seed, so an operator can
// supply the key out-of-band (flag or secret manager) instead of letting the
// service generate and store one. Accepts base64 (standard or URL, padded or
// not) and hex.
func SigningKeyFromSeed(encoded string) (SigningKey, error) {
	seed, err := decodeKeyMaterial(encoded)
	if err != nil {
		return SigningKey{}, fmt.Errorf("audit: decoding signing key seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return SigningKey{}, fmt.Errorf("audit: signing key seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub, _ := priv.Public().(ed25519.PublicKey)
	return SigningKey{ID: SigningKeyID(pub), Public: pub, Private: priv}, nil
}

// SigningKeyFromPublic builds a verify-only key.
func SigningKeyFromPublic(pub ed25519.PublicKey) SigningKey {
	return SigningKey{ID: SigningKeyID(pub), Public: pub}
}

// SigningKeyID is the first 16 hex characters of the public key's digest. Short
// enough to quote in a report, long enough not to collide in practice.
func SigningKeyID(pub ed25519.PublicKey) string {
	sum := blake3.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// CanSign reports whether this key holds the private half.
func (k SigningKey) CanSign() bool { return len(k.Private) == ed25519.PrivateKeySize }

// Sign signs a sealing hash.
func (k SigningKey) Sign(sealingHash []byte) ([]byte, error) {
	if !k.CanSign() {
		return nil, fmt.Errorf("audit: signing key %q holds no private key", k.ID)
	}
	return ed25519.Sign(k.Private, sealingHash), nil
}

// Verify checks a seal signature.
func (k SigningKey) Verify(sealingHash, signature []byte) bool {
	if len(k.Public) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(k.Public, sealingHash, signature)
}

// PublicKeyBase64 renders the public key for publication — this is the value an
// auditor is given, and the only value they need.
func (k SigningKey) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(k.Public)
}

// SeedBase64 renders the private seed so a generated key can be exported once
// and thereafter supplied by configuration.
func (k SigningKey) SeedBase64() string {
	if !k.CanSign() {
		return ""
	}
	return base64.StdEncoding.EncodeToString(k.Private.Seed())
}

func decodeKeyMaterial(encoded string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(encoded); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(encoded); err == nil {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(encoded); err == nil {
		return b, nil
	}
	b, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("value is neither valid base64 nor hex")
	}
	return b, nil
}
