package ledgerstore

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
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

func getContractVersion(md map[string]*commonpb.MetadataValue) models.ContractVersion {
	v, err := strconv.Atoi(getStr(md, schema.MetaContractVersion))
	if err != nil || v == 0 {
		// Absent means "written before the stamp existed". That used to decode as
		// V1; with V1 retired it resolves to the live contract, so such a record
		// stays visible rather than being filtered out of every list forever.
		return models.ContractVersionV2
	}
	return models.ContractVersion(v)
}

// --- Rule <-> metadata ---

// ruleToMetadata serialises a Rule to the typed control-ledger metadata map.
func ruleToMetadata(r *models.Rule) (map[string]*commonpb.MetadataValue, error) {
	md := map[string]*commonpb.MetadataValue{
		schema.MetaName:            strVal(r.Name),
		schema.MetaTemplateKind:    strVal(string(r.TemplateKind)),
		schema.MetaSpec:            strVal(string(r.TemplateSpec)),
		schema.MetaEnabled:         boolVal(r.Enabled),
		schema.MetaSeverity:        strVal(string(r.Severity)),
		schema.MetaPeriodType:      strVal(string(r.PeriodType)),
		schema.MetaCreatedAt:       dtVal(r.CreatedAt),
		schema.MetaUpdatedAt:       dtVal(r.UpdatedAt),
		schema.MetaContractVersion: strVal(strconv.Itoa(int(r.ContractVersion.Effective()))),
		schema.MetaRevision:        strVal(r.Revision),
	}

	if r.CompiledCEL != "" {
		md[schema.MetaCompiledCEL] = strVal(r.CompiledCEL)
	}

	if r.Schedule != nil {
		b, err := json.Marshal(r.Schedule)
		if err != nil {
			return nil, fmt.Errorf("marshal schedule: %w", err)
		}

		md[schema.MetaSchedule] = strVal(string(b))
	}

	if len(r.Notifications) > 0 {
		b, err := json.Marshal(r.Notifications)
		if err != nil {
			return nil, fmt.Errorf("marshal notifications: %w", err)
		}

		md[schema.MetaNotifications] = strVal(string(b))
	}

	for k, v := range r.Labels {
		md[schema.LabelPrefix+k] = strVal(v)
	}

	return md, nil
}

// isRuleAccount reports whether a `rule:*` account's metadata describes a live
// rule, rather than residue left by a non-rule writer.
//
// RecordCapture stamps the liveness keys onto the rule account without first
// reading it (that is the point — no extra round-trip). An evaluation still in
// flight when DeleteRule runs therefore writes those two keys back onto the
// just-emptied account, and a rule whose every identifying field is gone would
// otherwise decode as a nameless ghost that GetRule returns instead of 404 and
// ListRules shows forever. created_at is written unconditionally by
// ruleToMetadata and by nothing else, so its presence is the existence marker.
func isRuleAccount(md map[string]*commonpb.MetadataValue) bool {
	return !getTime(md, schema.MetaCreatedAt).IsZero()
}

// ruleFromAccount rebuilds a Rule from its control-ledger account (metadata +
// address, which carries the UUID).
func ruleFromAccount(acct *commonpb.Account) (*models.Rule, error) {
	id, err := schema.ParseRuleAccount(acct.GetAddress())
	if err != nil {
		return nil, err
	}

	md := acct.GetMetadata()

	r := &models.Rule{
		ID:              id,
		ContractVersion: getContractVersion(md),
		Revision:        getStr(md, schema.MetaRevision),
		Name:            getStr(md, schema.MetaName),
		TemplateKind:    models.TemplateKind(getStr(md, schema.MetaTemplateKind)),
		TemplateSpec:    json.RawMessage(getStr(md, schema.MetaSpec)),
		CompiledCEL:     getStr(md, schema.MetaCompiledCEL),
		Enabled:         getBool(md, schema.MetaEnabled),
		Severity:        models.Severity(getStr(md, schema.MetaSeverity)),
		PeriodType:      models.PeriodType(getStr(md, schema.MetaPeriodType)),
		CreatedAt:       getTime(md, schema.MetaCreatedAt),
		UpdatedAt:       getTime(md, schema.MetaUpdatedAt),
		LastVerdict:     getStr(md, schema.MetaLastVerdict),
	}

	// Absent means never evaluated. Left nil rather than zero-valued so a caller
	// cannot mistake "no run yet" for a run at the zero instant.
	if t := getTime(md, schema.MetaLastEvaluatedAt); !t.IsZero() {
		r.LastEvaluatedAt = &t
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
		if label, ok := labelKey(k); ok {
			if r.Labels == nil {
				r.Labels = map[string]string{}
			}

			r.Labels[label] = v.GetStringValue()
		}
	}
	if r.Revision == "" {
		r.Revision, err = ruleRevision(r)
		if err != nil {
			return nil, err
		}
	}

	return r, nil
}
