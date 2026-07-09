package ledgerschema

import "github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"

// Account-type names (used as the AccountType.Name / the "family" identifier).
const (
	AccountTypeRule        = "rule"
	AccountTypeAlertItem   = "alert-item"
	AccountTypeAlertState  = "alert-state"
	AccountTypeAlertPool   = "alert-pool"
	AccountTypeCapture     = "capture"      // per-(rule,period) capture bucket (ADR-003)
	AccountTypeCapturePool = "capture-pool" // per-rule capture mint source
)

// Capture transaction metadata keys — the self-describing capture envelope stamped
// on each evaluation's capture transaction (ADR-003). Transaction-level
// (COMMITTED_TRANSACTION payload), undeclared like the alert labels: stored as-is,
// not indexed. The immutable, receipt-signed transaction is the audit record.
const (
	CaptureType     = "reconciliation.capture" // value of CaptureMetaType
	CaptureMetaType = "type"
	CaptureMetaRule = "rule_id"
	CaptureMetaTmpl = "template_kind"
	CaptureMetaPer  = "period"
	CaptureMetaEval = "evaluation_id"
	CaptureMetaAt   = "captured_at"
	CaptureMetaVdt  = "verdict"
	CaptureMetaTrig = "trigger"
	CaptureMetaEvi  = "evidence"
)

// Metadata keys on the canonical alert (`alert:item:*`) account.
const (
	MetaID               = "id"     // alert UUID (indexed) — the Store addresses alerts by id
	MetaStatus           = "status" // mirror of the marker position (LWW, for O(1) point-read)
	MetaSeverity         = "severity"
	MetaRuleID           = "rule_id"
	MetaFingerprint      = "fingerprint" // raw fingerprint (address carries the hash)
	MetaPeriod           = "period"
	MetaFirstSeenAt      = "first_seen_at"
	MetaLastSeenAt       = "last_seen_at"
	MetaLastEvaluationID = "last_evaluation_id"
	MetaEvidence         = "evidence"   // JSON (inline; evidence-by-reference is a future optimization)
	MetaResolution       = "resolution" // JSON
	MetaAck              = "ack"        // JSON
	MetaSnooze           = "snooze"     // JSON
	// MetaLastTransition is a self-describing transition envelope (JSON) stamped
	// on every alert transition so the ledger log event for that write carries
	// "what happened" for event-sink consumers (RFC §4.4). Undeclared like the
	// labels — stored as string as-is, not queried.
	MetaLastTransition = "last_transition"
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
			Pattern:      "alert:item:rule:{ruleId}:per:{period}:fp:{fpHash}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid(), "period": period(), "fpHash": fpHash()},
		},
		AccountTypeAlertState: {
			Name:        AccountTypeAlertState,
			Pattern:     "alert:st:{state}:rule:{ruleId}:per:{period}:fp:{fpHash}",
			Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL,
			SegmentTypes: map[string]*commonpb.SegmentType{
				// Only active states hold a marker; on close the marker is burned
				// back to the pool and the account purges (EPHEMERAL). "resolved"
				// is a status-mirror value on the item, not a marker location.
				"state":  rgx(`^(open|ack)$`),
				"ruleId": uuid(), "period": period(), "fpHash": fpHash(),
			},
		},
		AccountTypeAlertPool: {
			// Per-rule overdraft source: mints ALERT markers and OCC counter units
			// (independent per-asset balances) for all of the rule's periods. Keyed
			// by rule only → O(#rules) source accounts.
			Name:         AccountTypeAlertPool,
			Pattern:      "alert:pool:rule:{ruleId}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid()},
		},
		AccountTypeCapture: {
			// Per-(rule,period) capture bucket: each evaluation records an immutable
			// capture transaction here (ADR-003). Holds the CAPTURE counter; the
			// observed snapshot is on each transaction's metadata.
			Name:         AccountTypeCapture,
			Pattern:      "capture:rule:{ruleId}:per:{period}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid(), "period": period()},
		},
		AccountTypeCapturePool: {
			// Per-rule overdraft source minting the CAPTURE marker (O(#rules)).
			Name:         AccountTypeCapturePool,
			Pattern:      "capture:pool:rule:{ruleId}",
			Persistence:  commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL,
			SegmentTypes: map[string]*commonpb.SegmentType{"ruleId": uuid()},
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
		{MetaID, str}, {MetaStatus, str}, {MetaSeverity, str}, {MetaRuleID, str}, {MetaFingerprint, str},
		{MetaPeriod, str}, {MetaFirstSeenAt, dt}, {MetaLastSeenAt, dt}, {MetaLastEvaluationID, str},
		{MetaEvidence, str}, {MetaResolution, str}, {MetaAck, str}, {MetaSnooze, str},
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

// MetadataIndexes returns the account-metadata secondary indexes to create at
// provisioning. SetMetadataFieldType declares a field's TYPE but does NOT make
// it queryable — a `metadata[k] <op> v` filter needs an explicit index, created
// via CreateIndex. This set covers every field the ListRules/ListAlerts filter
// translator can query on (the equality keys + the datetime range keys); `id`
// additionally backs id→address resolution (GetAlert/Ack/…). Dynamic `label.*`
// keys are intentionally not indexed (unbounded key space).
func MetadataIndexes() []*commonpb.IndexID {
	keys := []string{
		// alert:item filter + resolution fields
		MetaID, MetaStatus, MetaSeverity, MetaRuleID, MetaPeriod, MetaFingerprint,
		MetaFirstSeenAt, MetaLastSeenAt,
		// rule filter fields
		MetaName, MetaTemplateKind, MetaEnabled, MetaCreatedAt, MetaUpdatedAt,
	}

	idxs := make([]*commonpb.IndexID, 0, len(keys))
	for _, k := range keys {
		idxs = append(idxs, commonpb.AccountMetadataIndexID(k))
	}

	return idxs
}

// TransactionIndexes returns the transaction built-in indexes to create at
// provisioning. Listing transactions by address — ListCaptures scans a rule's
// capture bucket (`capture:rule:{ruleId}:per:*`) to read its evaluation history —
// requires the account→transaction address index (any role). Without it the
// ledger rejects an address-filtered transaction query with FailedPrecondition
// ("index not found: address").
func TransactionIndexes() []*commonpb.IndexID {
	return []*commonpb.IndexID{
		commonpb.TxBuiltinIndexID(commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ADDRESS),
	}
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
