package ledgerschema

import "github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"

// Account-type names (used as the AccountType.Name / the "family" identifier).
const (
	AccountTypeRule        = "rule"
	AccountTypeAlertItem   = "alert-item"
	AccountTypeAlertState  = "alert-state"
	AccountTypeAlertIssued = "alert-issued"
	AccountTypeAlertOcc    = "alert-occ"
)

// Metadata keys on the canonical alert (`alert:item:*`) account.
const (
	MetaStatus           = "status" // mirror of the marker position (LWW, for O(1) point-read)
	MetaSeverity         = "severity"
	MetaRuleID           = "rule_id"
	MetaFingerprint      = "fingerprint" // raw fingerprint (address carries the hash)
	MetaPeriod           = "period"
	MetaFirstSeenAt      = "first_seen_at"
	MetaLastSeenAt       = "last_seen_at"
	MetaLastEvaluationID = "last_evaluation_id"
	MetaEvidenceRef      = "evidence_ref"
	MetaResolution       = "resolution" // JSON
	MetaAck              = "ack"        // JSON
	MetaSnoozeUntil      = "snooze_until"
	MetaReopenedAt       = "reopened_at"
	MetaParentResolution = "parent_resolution"
)

// Metadata keys on the rule (`rule:*`) account.
const (
	MetaName          = "name"
	MetaTemplateKind  = "template_kind"
	MetaEnabled       = "enabled"
	MetaSchedule      = "schedule" // JSON
	MetaCadence       = "cadence"
	MetaSpec          = "spec" // JSON
	MetaCompiledCEL   = "compiled_cel"
	MetaNotifications = "notifications" // JSON array
	MetaCreatedAt     = "created_at"
	MetaUpdatedAt     = "updated_at"
)

// LabelPrefix namespaces a rule/alert label as a flat, indexable metadata key
// (e.g. label `env=prod` → metadata key `label.env`). Dynamic — not declared in
// MetadataSchema; stored as string as-is.
const LabelPrefix = "label."

// AccountTypes returns the chart of accounts declared on the control-ledger.
// Applied at bootstrap; combined with STRICT enforcement, any write to an
// address outside these patterns is rejected by the FSM (see RFC §4.1.3).
func AccountTypes() map[string]*commonpb.AccountType {
	uuid := func() *commonpb.SegmentType {
		return &commonpb.SegmentType{Constraint: &commonpb.SegmentType_Uuid{Uuid: &commonpb.UUIDConstraint{}}}
	}
	fpHash := func() *commonpb.SegmentType {
		return &commonpb.SegmentType{Constraint: &commonpb.SegmentType_Bytes{Bytes: &commonpb.BytesConstraint{}}}
	}
	rgx := func(p string) *commonpb.SegmentType {
		return &commonpb.SegmentType{Constraint: &commonpb.SegmentType_Regex{Regex: p}}
	}
	period := func() *commonpb.SegmentType { return rgx(`^[0-9A-Za-z-]+$`) }

	return map[string]*commonpb.AccountType{
		AccountTypeRule: {
			Name:         AccountTypeRule,
			Pattern:      "rule:{ruleId}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid()},
		},
		AccountTypeAlertItem: {
			Name:         AccountTypeAlertItem,
			Pattern:      "alert:item:rule:{ruleId}:fp:{fpHash}:per:{period}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid(), "fpHash": fpHash(), "period": period()},
		},
		AccountTypeAlertState: {
			Name:        AccountTypeAlertState,
			Pattern:     "alert:st:{state}:rule:{ruleId}:fp:{fpHash}:per:{period}",
			Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL,
			SegmentTypes: map[string]*commonpb.SegmentType{
				"state":  rgx(`^(open|ack|resolved|accepted)$`),
				"ruleId": uuid(), "fpHash": fpHash(), "period": period(),
			},
		},
		AccountTypeAlertIssued: {
			Name:         AccountTypeAlertIssued,
			Pattern:      "alert:issued:rule:{ruleId}:per:{period}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid(), "period": period()},
		},
		AccountTypeAlertOcc: {
			Name:         AccountTypeAlertOcc,
			Pattern:      "alert:occ:rule:{ruleId}:per:{period}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid(), "period": period()},
		},
	}
}

// metadataField describes one typed metadata key (all target ACCOUNT here).
type metadataField struct {
	key string
	typ commonpb.MetadataType
}

// MetadataSchema returns the ledger-wide typed metadata declarations. Declaring
// a field builds its forward index, enabling metadata-filtered queries. Dynamic
// label.* keys are intentionally left undeclared (stored as string as-is).
func MetadataSchema() []*commonpb.SetMetadataFieldTypeCommand {
	const (
		str = commonpb.MetadataType_METADATA_TYPE_STRING
		dt  = commonpb.MetadataType_METADATA_TYPE_DATETIME
		b   = commonpb.MetadataType_METADATA_TYPE_BOOL
	)

	fields := []metadataField{
		// alert:item
		{MetaStatus, str}, {MetaSeverity, str}, {MetaRuleID, str}, {MetaFingerprint, str},
		{MetaPeriod, str}, {MetaFirstSeenAt, dt}, {MetaLastSeenAt, dt}, {MetaLastEvaluationID, str},
		{MetaEvidenceRef, str}, {MetaResolution, str}, {MetaAck, str}, {MetaSnoozeUntil, dt},
		{MetaReopenedAt, dt}, {MetaParentResolution, str},
		// rule
		{MetaName, str}, {MetaTemplateKind, str}, {MetaEnabled, b}, {MetaSchedule, str},
		{MetaCadence, str}, {MetaSpec, str}, {MetaCompiledCEL, str}, {MetaNotifications, str},
		{MetaCreatedAt, dt}, {MetaUpdatedAt, dt},
	}

	cmds := make([]*commonpb.SetMetadataFieldTypeCommand, 0, len(fields))
	for _, f := range fields {
		cmds = append(cmds, &commonpb.SetMetadataFieldTypeCommand{
			TargetType: commonpb.TargetType_TARGET_TYPE_ACCOUNT,
			Key:        f.key,
			Type:       f.typ,
		})
	}

	return cmds
}

// Prepared query names. Only fixed-shape hot queries are prepared; per-rule /
// per-status / label-filtered lists are built ad-hoc by the filter translator (step 4).
const (
	PQOpenCount    = "alerts-open-count" // AGGREGATE_VOLUMES(ALERT) over all open markers
	PQRulesEnabled = "rules-enabled"     // LIST enabled rules (scheduler)
)

// PreparedQueries returns the fixed-shape prepared queries to register at
// bootstrap, as ready-to-apply commonpb.PreparedQuery protos.
func PreparedQueries() []*commonpb.PreparedQuery {
	return []*commonpb.PreparedQuery{
		{
			// filterexpr: address == "alert:st:open:*"
			Name:   PQOpenCount,
			Target: commonpb.QueryTarget_QUERY_TARGET_ACCOUNTS,
			Filter: FilterAddressPrefix(OpenPrefix()),
		},
		{
			// filterexpr: address == "rule:*" and metadata[enabled] == true
			Name:   PQRulesEnabled,
			Target: commonpb.QueryTarget_QUERY_TARGET_ACCOUNTS,
			Filter: FilterAll(FilterAddressPrefix(RulePrefix()), FilterMetadataBool(MetaEnabled, true)),
		},
	}
}
