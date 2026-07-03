package ledgerstore

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
)

// --- typed MetadataValue constructors ---

func strVal(s string) *commonpb.MetadataValue {
	return &commonpb.MetadataValue{Type: &commonpb.MetadataValue_StringValue{StringValue: s}}
}

func boolVal(b bool) *commonpb.MetadataValue {
	return &commonpb.MetadataValue{Type: &commonpb.MetadataValue_BoolValue{BoolValue: b}}
}

func dtVal(t time.Time) *commonpb.MetadataValue {
	return &commonpb.MetadataValue{Type: &commonpb.MetadataValue_DatetimeValue{DatetimeValue: t.UnixMicro()}}
}

// --- typed getters (nil-safe: proto getters return zero on absent/nil) ---

func getStr(md map[string]*commonpb.MetadataValue, key string) string {
	return md[key].GetStringValue()
}

func getBool(md map[string]*commonpb.MetadataValue, key string) bool {
	return md[key].GetBoolValue()
}

func getTime(md map[string]*commonpb.MetadataValue, key string) time.Time {
	v, ok := md[key]
	if !ok || v == nil {
		return time.Time{}
	}

	return time.UnixMicro(v.GetDatetimeValue()).UTC()
}

// --- Rule <-> metadata ---

// ruleToMetadata serialises a Rule to the typed control-ledger metadata map.
func ruleToMetadata(r *models.Rule) map[string]*commonpb.MetadataValue {
	md := map[string]*commonpb.MetadataValue{
		schema.MetaName:         strVal(r.Name),
		schema.MetaTemplateKind: strVal(string(r.TemplateKind)),
		schema.MetaSpec:         strVal(string(r.TemplateSpec)),
		schema.MetaEnabled:      boolVal(r.Enabled),
		schema.MetaSeverity:     strVal(string(r.Severity)),
		schema.MetaCadence:      strVal(string(r.Cadence)),
		schema.MetaCreatedAt:    dtVal(r.CreatedAt),
		schema.MetaUpdatedAt:    dtVal(r.UpdatedAt),
	}

	if r.CompiledCEL != "" {
		md[schema.MetaCompiledCEL] = strVal(r.CompiledCEL)
	}

	if r.Schedule != nil {
		if b, err := json.Marshal(r.Schedule); err == nil {
			md[schema.MetaSchedule] = strVal(string(b))
		}
	}

	if len(r.Notifications) > 0 {
		if b, err := json.Marshal(r.Notifications); err == nil {
			md[schema.MetaNotifications] = strVal(string(b))
		}
	}

	for k, v := range r.Labels {
		md[schema.LabelPrefix+k] = strVal(v)
	}

	return md
}

// ruleFromAccount rebuilds a Rule from its control-ledger account (metadata +
// address, which carries the UUID).
func ruleFromAccount(acct *commonpb.Account) (*models.Rule, error) {
	id, err := uuid.Parse(strings.TrimPrefix(acct.GetAddress(), "rule:"))
	if err != nil {
		return nil, err
	}

	md := acct.GetMetadata()

	r := &models.Rule{
		ID:           id,
		Name:         getStr(md, schema.MetaName),
		TemplateKind: models.TemplateKind(getStr(md, schema.MetaTemplateKind)),
		TemplateSpec: json.RawMessage(getStr(md, schema.MetaSpec)),
		CompiledCEL:  getStr(md, schema.MetaCompiledCEL),
		Enabled:      getBool(md, schema.MetaEnabled),
		Severity:     models.Severity(getStr(md, schema.MetaSeverity)),
		Cadence:      models.Cadence(getStr(md, schema.MetaCadence)),
		CreatedAt:    getTime(md, schema.MetaCreatedAt),
		UpdatedAt:    getTime(md, schema.MetaUpdatedAt),
	}

	if s := getStr(md, schema.MetaSchedule); s != "" {
		var sched models.Schedule
		if err := json.Unmarshal([]byte(s), &sched); err != nil {
			return nil, err
		}

		r.Schedule = &sched
	}

	if s := getStr(md, schema.MetaNotifications); s != "" {
		if err := json.Unmarshal([]byte(s), &r.Notifications); err != nil {
			return nil, err
		}
	}

	for k, v := range md {
		if label, ok := strings.CutPrefix(k, schema.LabelPrefix); ok {
			if r.Labels == nil {
				r.Labels = map[string]string{}
			}

			r.Labels[label] = v.GetStringValue()
		}
	}

	return r, nil
}
