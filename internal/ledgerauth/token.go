// Package ledgerauth provides the secure gRPC transport for the ledger client:
// Ed25519 JWT request signing (PerRPCCredentials) and TLS credentials, plus the
// assembly that refuses an insecure transport unless explicitly opted in (F2).
//
// Adapted from ledger-connect's internal/auth (the reference implementation);
// named ledgerauth here to avoid colliding with go-libs/auth (the HTTP
// server-side authenticator).
package ledgerauth

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// TokenProvider generates JWT bearer tokens signed with an Ed25519 private key.
// The tokens are passed via gRPC metadata to authenticate with the ledger.
// Emitted tokens carry the god=true claim, bypassing scope checks server-side.
type TokenProvider struct {
	signer  jose.Signer
	subject string
	ttl     time.Duration
}

// NewTokenProvider creates a token provider from a key ID, Ed25519 private key, and subject.
func NewTokenProvider(keyID string, privateKey ed25519.PrivateKey, subject string) (*TokenProvider, error) {
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: privateKey},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		return nil, fmt.Errorf("create JWT signer: %w", err)
	}

	return &TokenProvider{
		signer:  sig,
		subject: subject,
		ttl:     5 * time.Minute,
	}, nil
}

// NewTokenProviderFromFile loads an Ed25519 private key from a seed file.
// Accepts either a raw 32-byte seed or a 64-character hex-encoded seed.
func NewTokenProviderFromFile(keyID, path, subject string) (*TokenProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	seed, err := parseSeed(data)
	if err != nil {
		return nil, err
	}

	return NewTokenProvider(keyID, ed25519.NewKeyFromSeed(seed), subject)
}

// godClaim adds the `god` claim to JWTs, bypassing server-side scope checks.
type godClaim struct {
	God bool `json:"god"`
}

// Token generates a fresh JWT signed with EdDSA (Ed25519), with god=true claim.
func (tp *TokenProvider) Token() (string, error) {
	now := time.Now()

	std := jwt.Claims{
		Subject:  tp.subject,
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(tp.ttl)),
	}

	return jwt.Signed(tp.signer).Claims(std).Claims(godClaim{God: true}).Serialize()
}

// GetRequestMetadata implements grpc.PerRPCCredentials.
// It generates a fresh JWT for each RPC call.
func (tp *TokenProvider) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	token, err := tp.Token()
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"authorization": "Bearer " + token,
	}, nil
}

// RequireTransportSecurity implements grpc.PerRPCCredentials.
// Returns false to allow use with insecure connections (development).
func (tp *TokenProvider) RequireTransportSecurity() bool {
	return false
}

// parseSeed interprets file data as either raw 32-byte seed or hex-encoded seed.
func parseSeed(data []byte) ([]byte, error) {
	if len(data) == ed25519.SeedSize {
		return data, nil
	}

	// Try hex-encoded (64 hex chars, possibly with trailing newline).
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == ed25519.SeedSize*2 {
		seed, err := hex.DecodeString(trimmed)
		if err == nil && len(seed) == ed25519.SeedSize {
			return seed, nil
		}
	}

	return nil, fmt.Errorf("key file must be %d raw bytes or %d hex characters, got %d bytes",
		ed25519.SeedSize, ed25519.SeedSize*2, len(data))
}
