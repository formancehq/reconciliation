package ledgerstore

import (
	"context"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
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

// ListAlertEvents is implemented in alert_events.go: it projects the alert's
// lifecycle history from the control ledger's activity stream.
