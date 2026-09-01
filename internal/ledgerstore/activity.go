package ledgerstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
)

func ruleRevision(r *models.Rule) (string, error) {
	spec := r.TemplateSpec
	if len(spec) == 0 {
		spec = json.RawMessage(`null`)
	}
	config := struct {
		Name          string              `json:"name"`
		TemplateKind  models.TemplateKind `json:"templateKind"`
		TemplateSpec  json.RawMessage     `json:"templateSpec"`
		CompiledCEL   string              `json:"compiledCEL"`
		Enabled       bool                `json:"enabled"`
		Severity      models.Severity     `json:"severity"`
		PeriodType    models.PeriodType   `json:"periodType"`
		Schedule      *models.Schedule    `json:"schedule,omitempty"`
		Notifications []string            `json:"notifications,omitempty"`
		Labels        map[string]string   `json:"labels,omitempty"`
	}{r.Name, r.TemplateKind, spec, r.CompiledCEL, r.Enabled, r.Severity, r.PeriodType, r.Schedule, r.Notifications, r.Labels}
	b, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func activityVars(ruleID string) map[string]string {
	return map[string]string{schema.VarActivityPool: schema.ActivityPool(ruleID), schema.VarActivity: schema.ActivityAccount(ruleID)}
}

func addActivityVars(vars map[string]string, ruleID string) map[string]string {
	for k, v := range activityVars(ruleID) {
		vars[k] = v
	}
	return vars
}

func activityMetadata(kind string, ruleID uuid.UUID, version models.ContractVersion, revision, correlation string, at time.Time, payload any) (map[string]*commonpb.MetadataValue, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return map[string]*commonpb.MetadataValue{
		schema.ActivityMetaSchemaVersion: strVal("1"), schema.ActivityMetaKind: strVal(kind), schema.ActivityMetaRule: strVal(ruleID.String()),
		schema.ActivityMetaAt: strVal(at.UTC().Format(time.RFC3339Nano)), schema.ActivityMetaContractVersion: strVal(strconv.Itoa(int(version.Effective()))),
		schema.ActivityMetaRevision: strVal(revision), schema.ActivityMetaCorrelation: strVal(correlation), schema.ActivityMetaPayload: strVal(string(b)),
	}, nil
}

func activityFromTransaction(tx *commonpb.Transaction) (models.RuleActivity, bool) {
	md := tx.GetMetadata()
	kind := getStr(md, schema.ActivityMetaKind)
	if kind == "" {
		return models.RuleActivity{}, false
	}
	id, err := uuid.Parse(getStr(md, schema.ActivityMetaRule))
	if err != nil {
		return models.RuleActivity{}, false
	}
	version := models.ContractVersionV1
	if v, err := strconv.Atoi(getStr(md, schema.ActivityMetaContractVersion)); err == nil && v > 0 {
		version = models.ContractVersion(v)
	}
	at, _ := time.Parse(time.RFC3339Nano, getStr(md, schema.ActivityMetaAt))
	category, _, _ := strings.Cut(kind, ".")
	seq := strconv.FormatUint(tx.GetId(), 10)
	recordedAt := at.UTC()
	if ts := tx.GetInsertedAt(); ts != nil && ts.GetData() != 0 {
		recordedAt = time.UnixMicro(int64(ts.GetData())).UTC()
	}
	return models.RuleActivity{ID: seq + ":0", Sequence: seq, Kind: kind, Category: category, RuleID: id, ContractVersion: version, RuleRevision: getStr(md, schema.ActivityMetaRevision), CorrelationID: getStr(md, schema.ActivityMetaCorrelation), OccurredAt: at.UTC(), RecordedAt: recordedAt, Payload: json.RawMessage(getStr(md, schema.ActivityMetaPayload))}, true
}

func (s *LedgerStore) ListRuleActivities(ctx context.Context, ruleID uuid.UUID, q store.GetRuleActivitiesQuery) (*bunpaginate.Cursor[models.RuleActivity], error) {
	items := make([]models.RuleActivity, 0, 64)
	seenOtherContract := false
	if err := s.client.ListTransactionsFunc(ctx, s.controlLedger, schema.FilterAddressPrefix(schema.ActivityAccount(ruleID.String())), func(tx *commonpb.Transaction) error {
		if item, ok := activityFromTransaction(tx); ok {
			if q.Options.Options.ContractVersion != nil && item.ContractVersion.Effective() != *q.Options.Options.ContractVersion {
				seenOtherContract = true
				return nil
			}
			items = append(items, item)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list activities for rule %s: %w", ruleID, err)
	}
	if len(items) == 0 {
		if seenOtherContract {
			return nil, fmt.Errorf("list activities for rule %s: %w", ruleID, store.ErrNotFound)
		}
		rule, err := s.GetRule(ctx, ruleID)
		if err != nil {
			return nil, err
		}
		if version := q.Options.Options.ContractVersion; version != nil && rule.ContractVersion.Effective() != *version {
			return nil, fmt.Errorf("list activities for rule %s: %w", ruleID, store.ErrNotFound)
		}
	}
	slices.SortFunc(items, func(a, b models.RuleActivity) int {
		if c := b.OccurredAt.Compare(a.OccurredAt); c != 0 {
			return c
		}
		as, _ := strconv.ParseUint(a.Sequence, 10, 64)
		bs, _ := strconv.ParseUint(b.Sequence, 10, 64)
		if bs < as {
			return -1
		}
		if bs > as {
			return 1
		}
		return 0
	})
	data, more := paginateSlice(items, q.Offset, q.PageSize)
	return offsetCursor(bunpaginate.OffsetPaginatedQuery[store.PaginatedQueryOptions[store.RuleActivitiesFilters]](q), data, more), nil
}
