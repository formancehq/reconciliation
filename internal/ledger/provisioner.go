package ledger

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
)

//go:generate mockgen -source provisioner.go -destination provisioner_generated.go -package ledger . provisionAPI

// provisionAPI is the subset of *Client the Provisioner needs, kept small so it
// is trivially mockable for unit tests. *Client satisfies it.
type provisionAPI interface {
	CreateLedger(ctx context.Context, name string, schema []*commonpb.SetMetadataFieldTypeCommand, accountTypes map[string]*commonpb.AccountType, enforcement commonpb.ChartEnforcementMode) error
	GetLedgerInfo(ctx context.Context, name string) (*commonpb.LedgerInfo, error)
	AddAccountType(ctx context.Context, ledger string, accountType *commonpb.AccountType) error
	SetMetadataFieldType(ctx context.Context, ledger string, cmd *commonpb.SetMetadataFieldTypeCommand) error
	CreateIndex(ctx context.Context, ledger string, index *servicepb.CreateIndexRequest) error
	CreatePreparedQuery(ctx context.Context, ledger string, query *commonpb.PreparedQuery) error
	SaveNumscript(ctx context.Context, ledger, name, content, version string) error
}

// Compile-time proof the concrete client satisfies the provisioner's dependency,
// so any signature drift fails here rather than at wiring time (F10).
var _ provisionAPI = (*Client)(nil)

// Provisioner ensures the control-ledger exists with reconciliation's chart of
// accounts, typed metadata schema and prepared queries. Idempotent — safe to run
// on every boot.
type Provisioner struct {
	client      provisionAPI
	ledger      string
	enforcement commonpb.ChartEnforcementMode
}

// NewProvisioner builds a Provisioner for the given control-ledger. Start with
// AUDIT enforcement during rollout, then flip to STRICT once the chart is stable
// (RFC §4.1.3).
func NewProvisioner(client provisionAPI, ledgerName string, enforcement commonpb.ChartEnforcementMode) *Provisioner {
	return &Provisioner{client: client, ledger: ledgerName, enforcement: enforcement}
}

// Provision applies the control-ledger chart of accounts, typed metadata schema,
// indexes, prepared queries and numscripts. Idempotent and safe on every boot.
//
// On first boot CreateLedger applies the full chart + schema atomically. On a
// re-provision it is a no-op (AlreadyExists swallowed) and the account-type /
// metadata-field reconcile passes below bring an already-created ledger up to the
// current chart (F8) — so an *additive* chart change (a new account type or
// metadata key) no longer requires recreating the ledger. Destructive evolution
// (removing/retyping a field, or changing an account type that already holds
// accounts) is out of scope: an orphaned declaration is harmless (no writes),
// and a real redefinition surfaces its error rather than being silently applied.
//
// The reconcile is DELTA-based: it reads the ledger's current chart and only
// declares what is missing or mistyped. This matters for metadata — the ledger
// bumps a field's forward_encoding_version (a forward-index rewrite) on EVERY
// SetMetadataFieldType, with no unchanged-type guard, so re-declaring an indexed
// field it already has would rewrite that index on every boot.
func (p *Provisioner) Provision(ctx context.Context) error {
	accountTypes := schema.AccountTypes()
	metaSchema := schema.MetadataSchema()

	if err := p.client.CreateLedger(ctx, p.ledger, metaSchema, accountTypes, p.enforcement); err != nil {
		return fmt.Errorf("create control-ledger %q: %w", p.ledger, err)
	}

	// Read the current chart so the reconcile applies only the delta. Nil-safe:
	// getters on a nil LedgerInfo yield nil maps → everything is treated missing.
	info, err := p.client.GetLedgerInfo(ctx, p.ledger)
	if err != nil {
		return fmt.Errorf("read control-ledger %q: %w", p.ledger, err)
	}

	existingTypes := info.GetAccountTypes()
	existingFields := info.GetMetadataSchema().GetAccountFields()

	// Reconcile account types: add only those the ledger is missing (additive).
	// AddAccountType still swallows AlreadyExists as a race/read-lag backstop.
	// Sorted for a deterministic apply order.
	for _, name := range slices.Sorted(maps.Keys(accountTypes)) {
		if _, ok := existingTypes[name]; ok {
			continue
		}

		if err := p.client.AddAccountType(ctx, p.ledger, accountTypes[name]); err != nil {
			return fmt.Errorf("add account type %q: %w", name, err)
		}
	}

	// Reconcile the typed metadata schema: declare a field only when missing or
	// its declared type differs (a genuine retype legitimately triggers the
	// rewrite; re-declaring an unchanged field would needlessly rewrite it).
	for _, cmd := range metaSchema {
		if cur, ok := existingFields[cmd.GetKey()]; ok && cur.GetType() == cmd.GetType() {
			continue
		}

		if err := p.client.SetMetadataFieldType(ctx, p.ledger, cmd); err != nil {
			return fmt.Errorf("set metadata field type %q: %w", cmd.GetKey(), err)
		}
	}

	// Account metadata indexes (list/filter + id resolution) and the transaction
	// address index (capture-history listing). CreateIndex swallows AlreadyExists,
	// so a new index added here reconciles onto an existing ledger on next boot.
	for _, idx := range slices.Concat(schema.MetadataIndexes(), schema.TransactionIndexes()) {
		if err := p.client.CreateIndex(ctx, p.ledger, &servicepb.CreateIndexRequest{Id: idx}); err != nil {
			return fmt.Errorf("create index %v: %w", idx, err)
		}
	}

	for _, q := range schema.PreparedQueries() {
		if err := p.client.CreatePreparedQuery(ctx, p.ledger, q); err != nil {
			return fmt.Errorf("register prepared query %q: %w", q.GetName(), err)
		}
	}

	for _, ns := range schema.Numscripts() {
		if err := p.client.SaveNumscript(ctx, p.ledger, ns.Name, ns.Content, ns.Version); err != nil {
			return fmt.Errorf("register numscript %q: %w", ns.Name, err)
		}
	}

	return nil
}
