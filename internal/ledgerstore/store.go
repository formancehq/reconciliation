// Package ledgerstore implements reconciliation's V1 storage surface (rules,
// alerts, evaluations) on a Ledger v3 control-ledger, as an alternative to the
// PostgreSQL store. During the migration it is composed with the Postgres store
// (legacy /policies methods stay on Postgres); see
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
	GetAccount(ctx context.Context, ledgerName, address string, checkpointID uint64) (*commonpb.Account, error)
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
