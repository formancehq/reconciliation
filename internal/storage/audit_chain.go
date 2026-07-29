package storage

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	logging "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/uptrace/bun"

	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
)

// AuditChainSettings is the operator-supplied half of the chain's key material.
type AuditChainSettings struct {
	// Pepper is an optional secret held outside the database. With one
	// configured, a database dump is no longer sufficient to forge chain
	// entries — the attacker would also need the pepper. Ledger V2 had no
	// equivalent, since it hashed inside a database function.
	Pepper string
	// SigningKeySeed is the optional 32-byte Ed25519 seed (base64 or hex) used
	// to sign period seals. Supplying it by configuration is the recommended
	// posture: the service then never stores a private key at all. When absent,
	// one is generated on first boot and stored, and the seed is logged once so
	// it can be moved into configuration.
	SigningKeySeed string
}

// InitAuditChain bootstraps the chain on the shared Storage instance, in place.
//
// In place because a single *Storage pointer is injected everywhere, and the
// alternative — a WithAuditChain copy — would leave every already-injected
// consumer pointing at a chainless storage that fails every journalled write.
// Called once from the storage module's start hook, after the migration check
// and before anything serves traffic, so there is no concurrent reader.
func (s *Storage) InitAuditChain(ctx context.Context, settings AuditChainSettings) error {
	configured, err := EnsureAuditChain(ctx, s, settings)
	if err != nil {
		return err
	}
	s.chain = configured.chain
	s.signingKey = configured.signingKey
	return nil
}

// auditChainConfigRow is the singleton row holding this installation's chain
// identity.
type auditChainConfigRow struct {
	bun.BaseModel `bun:"reconciliations.audit_chain_config"`

	Singleton          bool      `bun:"singleton,pk"`
	Salt               []byte    `bun:"salt,notnull"`
	KeyCheck           []byte    `bun:"key_check,notnull"`
	HasPepper          bool      `bun:"has_pepper,notnull"`
	SigningKeyID       string    `bun:"signing_key_id,nullzero"`
	SigningPublicKey   []byte    `bun:"signing_public_key,nullzero"`
	SigningPrivateSeed []byte    `bun:"signing_private_seed,nullzero"`
	CreatedAt          time.Time `bun:"created_at,nullzero"`
}

type auditSigningKeyRow struct {
	bun.BaseModel `bun:"reconciliations.audit_signing_key"`

	KeyID     string     `bun:"key_id,pk"`
	PublicKey []byte     `bun:"public_key,notnull"`
	CreatedAt time.Time  `bun:"created_at,nullzero"`
	RetiredAt *time.Time `bun:"retired_at,nullzero"`
}

// ErrAuditChainKeyMismatch is returned when the configured pepper does not
// reproduce the key the existing chain was written under.
//
// Fatal by design, mirroring the Ledger's fatal cluster-id mismatch. Continuing
// would append entries whose hashes no verification can reconcile with the ones
// already stored — a chain that reports itself broken forever, from a
// configuration mistake rather than from tampering. Refusing to start is the
// only outcome that keeps the signal meaningful.
var ErrAuditChainKeyMismatch = errors.New("audit chain key mismatch")

// EnsureAuditChain bootstraps or validates the installation's chain identity and
// returns a Storage that journals every state-bearing write.
//
// First boot generates the salt and, unless a seed was configured, a signing key.
// Later boots re-derive the key from the stored salt plus the configured pepper
// and refuse to continue if the result does not match what the chain was built
// with.
func EnsureAuditChain(ctx context.Context, s *Storage, settings AuditChainSettings) (*Storage, error) {
	var row auditChainConfigRow
	err := s.db.NewSelect().Model(&row).Limit(1).Scan(ctx)

	switch {
	case err == nil:
		key := audit.DeriveChainKey(row.Salt, settings.Pepper)
		if !bytes.Equal(audit.KeyCheck(key), row.KeyCheck) {
			return nil, fmt.Errorf(
				"%w: the configured pepper does not reproduce the key this chain was written under "+
					"(stored has_pepper=%t). Restore the original pepper, or reset the journal deliberately — "+
					"appending under a different key would leave the chain permanently unverifiable",
				ErrAuditChainKeyMismatch, row.HasPepper)
		}
		signingKey, err := resolveSigningKey(ctx, s, &row, settings)
		if err != nil {
			return nil, err
		}
		// Re-assert the published public key on every boot, not only on first
		// boot. The config row and the key row are written by separate
		// statements, so a crash between them would leave a key that signs seals
		// but is absent from /audit-signing-keys — every seal it signed then
		// reports an unknown signing key and becomes unverifiable, permanently,
		// because later boots take this fast path. The insert is a no-op when the
		// row is already there, so making it idempotent costs nothing and closes
		// the window wherever the crash happened.
		if err := recordSigningKey(ctx, s, signingKey); err != nil {
			return nil, err
		}
		return s.WithAuditChain(audit.NewChain(key), signingKey), nil

	case errors.Is(err, sql.ErrNoRows):
		return initAuditChain(ctx, s, settings)

	default:
		return nil, e("read audit chain config", err)
	}
}

// ErrAuditChainKeyLost is returned when a journal exists but its key material
// does not.
//
// key_check catches a wrong pepper against a surviving salt. It cannot catch the
// reverse — a replaced salt with surviving entries — because the check value is
// regenerated alongside the new salt, so it agrees with itself. Boot would
// succeed and every pre-existing entry would be permanently unverifiable, with
// the breakage surfacing only later, at verification time, looking exactly like
// tampering.
//
// Reachable without an attacker: a partial restore that brings back the entry
// table but not the config row, a cleanup script that truncates the wrong table,
// a migration run against a half-restored database.
var ErrAuditChainKeyLost = errors.New("audit chain key material is missing but the journal is not empty")

func initAuditChain(ctx context.Context, s *Storage, settings AuditChainSettings) (*Storage, error) {
	// Minting fresh key material is only safe when there is nothing to verify.
	// With entries already present, a new salt silently orphans all of them.
	existing, err := s.db.NewSelect().Model((*models.AuditEntry)(nil)).Count(ctx)
	if err != nil {
		return nil, e("count existing audit entries", err)
	}
	if existing > 0 {
		return nil, fmt.Errorf(
			"%w: %d entries are present but reconciliations.audit_chain_config is empty. "+
				"Generating a new key would leave every one of them unverifiable and indistinguishable from tampering. "+
				"Restore the config row from the same backup as the journal, or drop the journal deliberately if this is meant to be a fresh start",
			ErrAuditChainKeyLost, existing)
	}

	salt, err := audit.NewSalt()
	if err != nil {
		return nil, err
	}
	key := audit.DeriveChainKey(salt, settings.Pepper)

	var signingKey audit.SigningKey
	generated := false
	if settings.SigningKeySeed != "" {
		signingKey, err = audit.SigningKeyFromSeed(settings.SigningKeySeed)
		if err != nil {
			return nil, err
		}
	} else {
		signingKey, err = audit.GenerateSigningKey()
		if err != nil {
			return nil, err
		}
		generated = true
	}

	row := &auditChainConfigRow{
		Singleton:        true,
		Salt:             salt,
		KeyCheck:         audit.KeyCheck(key),
		HasPepper:        settings.Pepper != "",
		SigningKeyID:     signingKey.ID,
		SigningPublicKey: signingKey.Public,
	}
	if generated {
		// Stored only because we made it up ourselves; an operator-supplied seed
		// is never written to the database.
		row.SigningPrivateSeed = signingKey.Private.Seed()
	}

	if _, err := s.db.NewInsert().Model(row).
		On("CONFLICT (singleton) DO NOTHING").Exec(ctx); err != nil {
		return nil, e("initialise audit chain config", err)
	}

	// A concurrent replica may have won the insert. Re-read rather than assume,
	// so both replicas end up on the same key.
	var stored auditChainConfigRow
	if err := s.db.NewSelect().Model(&stored).Limit(1).Scan(ctx); err != nil {
		return nil, e("read audit chain config after init", err)
	}
	if !bytes.Equal(stored.Salt, salt) {
		return EnsureAuditChain(ctx, s, settings)
	}

	if err := recordSigningKey(ctx, s, signingKey); err != nil {
		return nil, err
	}

	log := logging.FromContext(ctx)
	log.WithFields(map[string]any{
		"signingKeyID": signingKey.ID,
		"publicKey":    signingKey.PublicKeyBase64(),
		"hasPepper":    row.HasPepper,
	}).Infof("reconciliation: audit journal initialised")

	if generated {
		// Deliberately NOT logging the seed. Logs usually have broader read
		// access and longer retention than the database, so writing the private
		// key there would invert the key-separation property this feature exists
		// to provide: anyone with log access could forge period seals. The seed is
		// in audit_chain_config.signing_private_seed for an operator to retrieve
		// deliberately, once, when they are ready to move it into configuration.
		log.WithFields(map[string]any{
			"signingKeyID": signingKey.ID,
			"publicKey":    signingKey.PublicKeyBase64(),
		}).Infof("reconciliation: generated a period-seal signing key and stored it in the database. " +
			"Move it into configuration (--audit-signing-key-seed) so the private key no longer lives next to the data it signs; " +
			"read the seed from reconciliations.audit_chain_config.signing_private_seed, then it can be cleared")
	}
	if !row.HasPepper {
		log.Infof("reconciliation: audit chain running without a pepper; the key is derived from database-resident salt alone, " +
			"so anyone with full database access could in principle rebuild the chain. Set --audit-chain-pepper to close that.")
	}

	return s.WithAuditChain(audit.NewChain(key), signingKey), nil
}

// resolveSigningKey decides which key signs new seals: a configured seed always
// wins over whatever the database holds, so moving the key into configuration is
// a one-way improvement that needs no migration.
func resolveSigningKey(ctx context.Context, s *Storage, row *auditChainConfigRow, settings AuditChainSettings) (audit.SigningKey, error) {
	if settings.SigningKeySeed != "" {
		key, err := audit.SigningKeyFromSeed(settings.SigningKeySeed)
		if err != nil {
			return audit.SigningKey{}, err
		}
		if key.ID != row.SigningKeyID {
			// A rotation. The previous key stays in audit_signing_key so the
			// seals it signed remain verifiable.
			if err := rotateSigningKey(ctx, s, row, key); err != nil {
				return audit.SigningKey{}, err
			}
		}
		return key, nil
	}

	if len(row.SigningPrivateSeed) == ed25519.SeedSize {
		priv := ed25519.NewKeyFromSeed(row.SigningPrivateSeed)
		pub, _ := priv.Public().(ed25519.PublicKey)
		return audit.SigningKey{ID: audit.SigningKeyID(pub), Public: pub, Private: priv}, nil
	}

	if len(row.SigningPublicKey) == ed25519.PublicKeySize {
		// Verify-only: the operator moved the seed out of the database but did
		// not supply it to this process. Sealing still works; the seal carries no
		// signature, and that absence is visible in the API rather than silently
		// implied.
		logging.FromContext(ctx).Infof(
			"reconciliation: no signing key available to this process — period seals will be recorded without a signature")
		return audit.SigningKeyFromPublic(row.SigningPublicKey), nil
	}

	return audit.SigningKey{}, nil
}

func rotateSigningKey(ctx context.Context, s *Storage, row *auditChainConfigRow, key audit.SigningKey) error {
	now := time.Now().UTC()
	if row.SigningKeyID != "" {
		if _, err := s.db.NewUpdate().Model((*auditSigningKeyRow)(nil)).
			Set("retired_at = ?", now).
			Where("key_id = ? AND retired_at IS NULL", row.SigningKeyID).Exec(ctx); err != nil {
			return e("retire previous signing key", err)
		}
	}
	if _, err := s.db.NewUpdate().Model((*auditChainConfigRow)(nil)).
		Set("signing_key_id = ?", key.ID).
		Set("signing_public_key = ?", []byte(key.Public)).
		Set("signing_private_seed = NULL").
		Where("singleton").Exec(ctx); err != nil {
		return e("record rotated signing key", err)
	}
	if err := recordSigningKey(ctx, s, key); err != nil {
		return err
	}
	logging.FromContext(ctx).WithFields(map[string]any{
		"previousKeyID": row.SigningKeyID,
		"signingKeyID":  key.ID,
	}).Infof("reconciliation: period-seal signing key rotated; seals signed by the previous key remain verifiable")
	return nil
}

func recordSigningKey(ctx context.Context, s *Storage, key audit.SigningKey) error {
	if len(key.Public) != ed25519.PublicKeySize {
		return nil
	}
	_, err := s.db.NewInsert().Model(&auditSigningKeyRow{
		KeyID:     key.ID,
		PublicKey: key.Public,
	}).On("CONFLICT (key_id) DO NOTHING").Exec(ctx)
	if err != nil {
		return e("record signing key", err)
	}
	return nil
}

// VerificationKey is a published public key an auditor uses to check seal
// signatures without involving this service.
type VerificationKey struct {
	KeyID      string     `json:"keyID"`
	PublicKey  string     `json:"publicKey"`
	Algorithm  string     `json:"algorithm"`
	Active     bool       `json:"active"`
	CreatedAt  time.Time  `json:"createdAt"`
	RetiredAt  *time.Time `json:"retiredAt,omitempty"`
	Thumbprint string     `json:"thumbprint"`
}

// thumbprintOf renders a short fingerprint without assuming a length. The
// public_key column is NOT NULL but has no length constraint, so a manual
// migration, a restored dump, or a future writer could store a shorter blob —
// and a slice-bounds panic in the endpoint an auditor calls is a poor trade for
// four saved characters.
func thumbprintOf(pub []byte) string {
	if len(pub) > 8 {
		pub = pub[:8]
	}
	return hex.EncodeToString(pub)
}

// ListVerificationKeys returns every key this installation has signed seals
// with, active first. Retired keys are kept and served: a seal from two years
// ago must stay checkable.
func (s *Storage) ListVerificationKeys(ctx context.Context) ([]VerificationKey, error) {
	var rows []auditSigningKeyRow
	if err := s.db.NewSelect().Model(&rows).
		Order("retired_at IS NOT NULL", "created_at DESC").Scan(ctx); err != nil {
		return nil, e("list verification keys", err)
	}
	out := make([]VerificationKey, 0, len(rows))
	for _, r := range rows {
		out = append(out, VerificationKey{
			KeyID:      r.KeyID,
			PublicKey:  audit.SigningKeyFromPublic(r.PublicKey).PublicKeyBase64(),
			Algorithm:  "Ed25519",
			Active:     r.RetiredAt == nil,
			CreatedAt:  r.CreatedAt,
			RetiredAt:  r.RetiredAt,
			Thumbprint: thumbprintOf(r.PublicKey),
		})
	}
	return out, nil
}
