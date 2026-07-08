package ledgerstore

import (
	"context"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
)

// pingTimeout bounds the health-probe read so Ping cannot hang on the gRPC
// retry policy when the ledger is unreachable.
const pingTimeout = 5 * time.Second

// Ping verifies the control-ledger is reachable by reading its `world` account
// (which exists in every ledger). Satisfies the Store health contract.
func (s *LedgerStore) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	_, err := s.client.GetAccount(ctx, s.controlLedger, "world")
	return err
}

// CreateEvaluation is a no-op in the ledger-native store: evaluations are a
// deterministic projection, not a durable entity (RFC §4.4.2). The run result
// is returned to the caller by the service, and the break evidence lives on the
// alert (Evidence + LastEvaluationID, written by the alert transitions). A
// queryable run history, if the product needs one, is a ledger event sink
// (Phase 3+), not a control-ledger account.
func (s *LedgerStore) CreateEvaluation(context.Context, *models.Evaluation) error {
	return nil
}

// ListAlertEvents returns an empty page. The alert transition history is the
// ordered SAVED_METADATA event stream of the `_recon` ledger (RFC §4.4); a
// queryable projection of it is delivered by a ledger event sink in Phase 3-4.
// Until that lands, this reader is intentionally empty — the machine and
// operator paths do not depend on it.
//
// TODO(phase-3): back this with the `_recon` SAVED_METADATA stream / sink.
func (s *LedgerStore) ListAlertEvents(context.Context, uuid.UUID, store.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error) {
	return &bunpaginate.Cursor[models.AlertEvent]{Data: []models.AlertEvent{}}, nil
}
