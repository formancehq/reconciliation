package ledgerstore

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
)

// transitionType is the alert lifecycle transition an event describes. It maps
// to the envelope's fully-qualified type (reconciliation.alert.<transitionType>).
type transitionType string

const (
	transitionOpened       transitionType = "opened"
	transitionOccurred     transitionType = "occurred"
	transitionReopened     transitionType = "reopened"
	transitionAcknowledged transitionType = "acknowledged"
	transitionResolved     transitionType = "resolved"
	transitionAccepted     transitionType = "accepted"
	transitionAutoResolved transitionType = "auto_resolved"
	transitionSnoozed      transitionType = "snoozed"
	transitionUnsnoozed    transitionType = "unsnoozed"
)

// transitionEventTypePrefix namespaces the envelope type so a sink consumer can
// route reconciliation events without parsing the metadata diff (RFC §4.4).
const transitionEventTypePrefix = "reconciliation.alert."

// transitionEvent is the self-describing envelope written to the item account's
// last_transition metadata on every transition. The ledger log entry for that
// write (COMMITTED_TRANSACTION for lifecycle moves, SAVED_METADATA/DELETED_METADATA
// for snooze) carries it, so an event-sink consumer sees "what happened" without
// reverse-engineering the metadata diff.
type transitionEvent struct {
	Type          string         `json:"type"`    // reconciliation.alert.<transitionType>
	Subject       string         `json:"subject"` // alert:{ruleID}:{fingerprint}
	AlertID       string         `json:"alertID"`
	PrevStatus    string         `json:"prevStatus,omitempty"`
	NewStatus     string         `json:"newStatus"`
	OccurredAt    time.Time      `json:"occurredAt"`
	CorrelationID string         `json:"correlationID,omitempty"` // evaluationID for eval-driven transitions
	Payload       map[string]any `json:"payload,omitempty"`
}

// stampTransition adds the self-describing last_transition envelope to an alert
// metadata map, so the same atomic write that mutates the alert also records the
// event. prev is the status before the transition (empty for a first open); a is
// the post-transition alert. correlationID is the evaluation id for eval-driven
// transitions, empty for operator actions.
func stampTransition(md map[string]*commonpb.MetadataValue, t transitionType, a *models.Alert, prev models.AlertStatus, correlationID string, at time.Time, payload map[string]any) error {
	env := transitionEvent{
		Type:          transitionEventTypePrefix + string(t),
		Subject:       fmt.Sprintf("alert:%s:%s", a.RuleID, a.Fingerprint),
		AlertID:       a.ID.String(),
		PrevStatus:    string(prev),
		NewStatus:     string(a.Status),
		OccurredAt:    at.UTC(),
		CorrelationID: correlationID,
		Payload:       payload,
	}

	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal transition event: %w", err)
	}

	md[schema.MetaLastTransition] = strVal(string(b))

	return nil
}

func alertActivityMetadata(a *models.Alert, md map[string]*commonpb.MetadataValue) (map[string]*commonpb.MetadataValue, error) {
	var env transitionEvent
	if err := json.Unmarshal([]byte(getStr(md, schema.MetaLastTransition)), &env); err != nil {
		return nil, err
	}
	kind := "alert." + string(env.Type[len(transitionEventTypePrefix):])
	return activityMetadata(kind, a.RuleID, a.ContractVersion, "", env.CorrelationID, env.OccurredAt, env)
}
