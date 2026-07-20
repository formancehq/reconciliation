package ledgerstore

import (
	"encoding/json"
	"fmt"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
)

// statusToState maps an AlertStatus to the marker's lifecycle-state segment.
// "accepted" is a resolution kind, not a status, so it maps to resolved.
func statusToState(s models.AlertStatus) string {
	switch s {
	case models.AlertOpen:
		return schema.StateOpen
	case models.AlertAcknowledged:
		return schema.StateAck
	case models.AlertResolved:
		return schema.StateResolved
	default:
		return ""
	}
}

// stateToStatus is the inverse of statusToState.
func stateToStatus(state string) models.AlertStatus {
	switch state {
	case schema.StateOpen:
		return models.AlertOpen
	case schema.StateAck:
		return models.AlertAcknowledged
	case schema.StateResolved:
		return models.AlertResolved
	default:
		return ""
	}
}

// alertToMetadata serialises the descriptive fields of an Alert to typed metadata
// for the `alert:item:*` account. OccurrenceCount is NOT included — it lives as
// the account's OCC balance. The lifecycle status lives both here (mirror, for
// O(1) reads) and as the marker position (source of truth + transition guard).
func alertToMetadata(a *models.Alert) (map[string]*commonpb.MetadataValue, error) {
	md := map[string]*commonpb.MetadataValue{
		schema.MetaID:               strVal(a.ID.String()),
		schema.MetaRuleID:           strVal(a.RuleID.String()),
		schema.MetaFingerprint:      strVal(a.Fingerprint),
		schema.MetaPeriod:           strVal(a.PeriodID),
		schema.MetaStatus:           strVal(string(a.Status)),
		schema.MetaSeverity:         strVal(string(a.Severity)),
		schema.MetaFirstSeenAt:      dtVal(a.FirstSeenAt),
		schema.MetaLastSeenAt:       dtVal(a.LastSeenAt),
		schema.MetaLastEvaluationID: strVal(a.LastEvaluationID.String()),
		schema.MetaCreatedAt:        dtVal(a.CreatedAt),
	}

	if len(a.Evidence) > 0 {
		md[schema.MetaEvidence] = strVal(string(a.Evidence))
	}

	for key, v := range map[string]any{
		schema.MetaAck:        a.Ack,
		schema.MetaResolution: a.Resolution,
		schema.MetaSnooze:     a.Snooze,
	} {
		set, err := marshalOptional(v)
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", key, err)
		}

		if set != "" {
			md[key] = strVal(set)
		}
	}

	for k, v := range a.Labels {
		md[schema.LabelPrefix+k] = strVal(v)
	}

	return md, nil
}

// alertFromAccount rebuilds an Alert from its item account (typed metadata +
// OccurrenceCount from the OCC balance).
func alertFromAccount(acct *commonpb.Account) (*models.Alert, error) {
	md := acct.GetMetadata()

	id, err := uuid.Parse(getStr(md, schema.MetaID))
	if err != nil {
		return nil, fmt.Errorf("alert id: %w", err)
	}

	ruleID, err := uuid.Parse(getStr(md, schema.MetaRuleID))
	if err != nil {
		return nil, fmt.Errorf("alert rule_id: %w", err)
	}

	a := &models.Alert{
		ID:              id,
		RuleID:          ruleID,
		Fingerprint:     getStr(md, schema.MetaFingerprint),
		PeriodID:        getStr(md, schema.MetaPeriod),
		Status:          models.AlertStatus(getStr(md, schema.MetaStatus)),
		Severity:        models.Severity(getStr(md, schema.MetaSeverity)),
		FirstSeenAt:     getTime(md, schema.MetaFirstSeenAt),
		LastSeenAt:      getTime(md, schema.MetaLastSeenAt),
		CreatedAt:       getTime(md, schema.MetaCreatedAt),
		OccurrenceCount: occurrenceCount(acct),
	}

	if s := getStr(md, schema.MetaLastEvaluationID); s != "" {
		if lid, err := uuid.Parse(s); err == nil {
			a.LastEvaluationID = lid
		}
	}

	if s := getStr(md, schema.MetaEvidence); s != "" {
		a.Evidence = json.RawMessage(s)
	}

	if err := unmarshalOptional(getStr(md, schema.MetaAck), &a.Ack); err != nil {
		return nil, fmt.Errorf("alert ack: %w", err)
	}

	if err := unmarshalOptional(getStr(md, schema.MetaResolution), &a.Resolution); err != nil {
		return nil, fmt.Errorf("alert resolution: %w", err)
	}

	if err := unmarshalOptional(getStr(md, schema.MetaSnooze), &a.Snooze); err != nil {
		return nil, fmt.Errorf("alert snooze: %w", err)
	}

	for k, v := range md {
		if label, ok := labelKey(k); ok {
			if a.Labels == nil {
				a.Labels = map[string]string{}
			}

			a.Labels[label] = v.GetStringValue()
		}
	}

	return a, nil
}

// occurrenceCount reads the alert's OCC balance, summing across colors since
// volumes are now reported per (asset, color). BalancesByAsset does the summing
// (and the input−output fallback) so color handling lives in one place.
func occurrenceCount(acct *commonpb.Account) int64 {
	occ := commonpb.BalancesByAsset(acct)[schema.AssetOcc]
	if occ == nil {
		return 0
	}

	return occ.Int64()
}

// marshalOptional JSON-encodes a possibly-nil pointer; returns "" for nil.
func marshalOptional(v any) (string, error) {
	switch p := v.(type) {
	case *models.Ack:
		if p == nil {
			return "", nil
		}
	case *models.Resolution:
		if p == nil {
			return "", nil
		}
	case *models.Snooze:
		if p == nil {
			return "", nil
		}
	}

	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// unmarshalOptional decodes s into dst (a **struct) when s is non-empty.
func unmarshalOptional(s string, dst any) error {
	if s == "" {
		return nil
	}

	return json.Unmarshal([]byte(s), dst)
}

// labelKey returns the label name for a `label.*` metadata key.
func labelKey(k string) (string, bool) {
	if len(k) > len(schema.LabelPrefix) && k[:len(schema.LabelPrefix)] == schema.LabelPrefix {
		return k[len(schema.LabelPrefix):], true
	}

	return "", false
}
