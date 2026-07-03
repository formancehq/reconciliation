package ledger

import (
	"context"
	"fmt"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
)

//go:generate mockgen -source provisioner.go -destination provisioner_generated.go -package ledger . provisionAPI

// provisionAPI is the subset of *Client the Provisioner needs, kept small so it
// is trivially mockable for unit tests. *Client satisfies it.
type provisionAPI interface {
	CreateLedger(ctx context.Context, name string, schema []*commonpb.SetMetadataFieldTypeCommand, accountTypes map[string]*commonpb.AccountType, enforcement commonpb.ChartEnforcementMode) error
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

// Provision applies the control-ledger chart of accounts, typed metadata schema
// and prepared queries. Idempotent (the client swallows AlreadyExists).
//
// NOTE: schema evolution on an already-created ledger (adding a metadata field or
// account type after first boot) is a tracked follow-up — see the migration log.
// On first boot the full schema is applied atomically via CreateLedger.
func (p *Provisioner) Provision(ctx context.Context) error {
	if err := p.client.CreateLedger(ctx, p.ledger, schema.MetadataSchema(), schema.AccountTypes(), p.enforcement); err != nil {
		return fmt.Errorf("create control-ledger %q: %w", p.ledger, err)
	}

	for _, idx := range schema.MetadataIndexes() {
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
