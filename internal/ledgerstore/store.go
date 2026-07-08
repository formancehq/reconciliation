// Package ledgerstore implements reconciliation's storage surface (rules,
// alerts) on a Ledger v3 control-ledger (`_recon`) — the sole Store, replacing
// PostgreSQL. Evaluations are non-durable (CreateEvaluation is a no-op) and
// alert-event history is deferred to a ledger event sink (adapters.go); see
// docs/drafts/rfc-ledger-native-storage.md.
package ledgerstore

import (
	"context"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
)

//go:generate mockgen -source store.go -destination store_generated.go -package ledgerstore . ledgerClient

// ledgerClient is the subset of *ledger.Client the store needs, kept small so it
// is trivially mockable. *ledger.Client satisfies it (asserted below).
type ledgerClient interface {
	SaveAccountMetadataValues(ctx context.Context, ledgerName, address string, md map[string]*commonpb.MetadataValue) error
	ApplyMetadata(ctx context.Context, ledgerName, address string, set map[string]*commonpb.MetadataValue, deleteKeys ...string) error
	DeleteAccountMetadata(ctx context.Context, ledgerName, address string, keys ...string) error
	GetAccount(ctx context.Context, ledgerName, address string) (*commonpb.Account, error)
	QueryAccounts(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter) ([]*commonpb.Account, error)
	CreateTransaction(ctx context.Context, in ledger.CreateTransactionInput) error
}

var _ ledgerClient = (*ledger.Client)(nil)

// LedgerStore persists reconciliation state in a control-ledger.
type LedgerStore struct {
	client        ledgerClient
	controlLedger string
}

// New builds a LedgerStore over the given control-ledger name.
func New(client ledgerClient, controlLedger string) *LedgerStore {
	return &LedgerStore{client: client, controlLedger: controlLedger}
}
