package ledgerstore

import (
	"context"
	"fmt"
	"math/big"

	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
)

// CountAlertsByRule returns each rule's live alert tally, in **one** call.
//
// The count is an aggregate of the ALERT markers, not a scan of alerts: a live
// alert holds exactly one marker unit at `alert:st:{state}:rule:{id}:per:…`, so
// the balance of a (state, rule) prefix *is* the number of alerts in that state.
// Resolved and accepted alerts have burned their marker back to the pool, so they
// are absent from both figures by construction rather than by filtering.
//
// Every rule contributes two group prefixes to a single AGGREGATE_VOLUMES, which
// is what keeps a page of rules at one round trip. A rule with no live alerts
// matches no marker account and comes back as an explicit zero.
func (s *LedgerStore) CountAlertsByRule(ctx context.Context, ruleIDs []uuid.UUID) (map[uuid.UUID]models.AlertCounts, error) {
	out := make(map[uuid.UUID]models.AlertCounts, len(ruleIDs))
	if len(ruleIDs) == 0 {
		return out, nil
	}

	prefixes := make([]string, 0, 2*len(ruleIDs))
	for _, id := range ruleIDs {
		out[id] = models.AlertCounts{}
		prefixes = append(prefixes,
			schema.StateByRulePrefix(schema.StateOpen, id.String()),
			schema.StateByRulePrefix(schema.StateAck, id.String()),
		)
	}

	groups, err := s.client.AggregateVolumesGrouped(ctx, s.controlLedger,
		schema.FilterAddressPrefix(schema.StatePrefix()), prefixes)
	if err != nil {
		return nil, fmt.Errorf("count alerts by rule: %w", err)
	}

	for _, id := range ruleIDs {
		counts := out[id]
		counts.Open = markerCount(groups[schema.StateByRulePrefix(schema.StateOpen, id.String())])
		counts.Acknowledged = markerCount(groups[schema.StateByRulePrefix(schema.StateAck, id.String())])
		out[id] = counts
	}

	return out, nil
}

// markerCount reads the ALERT balance of one aggregation group. A marker is one
// unit of a precision-zero asset, so the balance is a count; an absent group (the
// prefix matched nothing) is zero.
func markerCount(byAsset map[string]*big.Int) int64 {
	if byAsset == nil {
		return 0
	}

	balance := byAsset[schema.AssetAlert]
	if balance == nil {
		return 0
	}

	return balance.Int64()
}
