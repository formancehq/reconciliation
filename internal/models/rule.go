package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ContractVersion identifies the public HTTP contract that owns a persisted
// rule and all evidence derived from it. It is immutable for the lifetime of a
// rule. Records written before the field existed are V1.
type ContractVersion int

const (
	ContractVersionV1 ContractVersion = 1
	ContractVersionV2 ContractVersion = 2
)

func (v ContractVersion) Effective() ContractVersion {
	if v == 0 {
		return ContractVersionV1
	}
	return v
}

// TemplateKind identifies a typed catalog entry that compiles deterministically
// to an internal CEL expression. Contract-version validation controls which
// entries are available through V1 and V2; the kernel remains internal.
// See ADR-001 for the rationale.
type TemplateKind string

const (
	// TemplateLedgerInvariant asserts that a signed sum of balance(source)
	// terms is within tolerance — the Buildr-style trust integrity check.
	TemplateLedgerInvariant TemplateKind = "ledger_invariant"
	// TemplateAccountThreshold asserts that each (or aggregate) balance in
	// a ledger set is within [min, max] bounds, per asset.
	TemplateAccountThreshold TemplateKind = "account_threshold"
	// TemplateSourceParity asserts that two balance sources (ledger query or
	// payments pool — either side) agree within a per-asset tolerance. The
	// generalised "two independent records of the same money match" check;
	// see internal/templates/source.go.
	TemplateSourceParity TemplateKind = "source_parity"
	// TemplateBalanceEquation is the V2 N-source equality primitive. It asserts
	// that an integer-weighted sum of named balances is zero within tolerance.
	TemplateBalanceEquation TemplateKind = "balance_equation"
	// TemplateExchangeRateBounds is the V2 cross-asset rate primitive. It checks
	// the exact quote-major/base-major ratio against inclusive bounds.
	TemplateExchangeRateBounds TemplateKind = "exchange_rate_bounds"
	// TemplateSourceConsensus is the V2 symmetric N-source agreement primitive.
	TemplateSourceConsensus TemplateKind = "source_consensus"
	// TemplateCoverageRatioBounds compares exact signed multi-source portfolios.
	TemplateCoverageRatioBounds TemplateKind = "coverage_ratio_bounds"
)

// Severity is shared between Rule (declared severity at creation) and Alert
// (inherited from the rule, possibly escalated). The CHECK constraint in the
// migration mirrors this set verbatim.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// PeriodType is how long a reconciliation period lasts for a rule — it decides
// how a failing fingerprint is scoped into a period. A fresh alert is opened per
// period, so a March break and an April break of the same fingerprint are
// distinct, independently-closable cases; resolving April never rewrites March's
// record.
//
// This is not how often the rule runs — that is Schedule, and the two are
// independent: an hourly schedule with a monthly period type is normal.
// See docs/technical/alert-period-model.md.
type PeriodType string

const (
	// PeriodTypeContinuous is live monitoring: a single unbounded period. A
	// failing fingerprint reopens in place until resolved, like a classic
	// monitoring alert. Default — periodic scoping is opt-in per rule.
	PeriodTypeContinuous PeriodType = "continuous"
	// PeriodTypeDaily buckets cases by UTC calendar day (period id "2006-01-02").
	PeriodTypeDaily PeriodType = "daily"
	// PeriodTypeWeekly buckets cases by ISO week (period id "2026-W12"). The ISO
	// year can differ from the calendar year near year boundaries — ISOWeek()
	// returns the correct ISO year, so the bucket is unambiguous.
	PeriodTypeWeekly PeriodType = "weekly"
	// PeriodTypeMonthly buckets cases by UTC calendar month (period id "2006-01").
	PeriodTypeMonthly PeriodType = "monthly"
)

// ContinuousPeriod is the sentinel period id for PeriodTypeContinuous and the
// safe fallback for an unset/unknown period type: one unbounded scope, which
// reproduces the original (rule_id, fingerprint) dedup exactly.
const ContinuousPeriod = "continuous"

// PeriodID maps an evaluation's point-in-time to the period a failing
// fingerprint belongs to under this period type. Deterministic: any instant in
// the same bucket yields the same id, so re-evaluating a period continues its
// existing case rather than spawning a new one. Bucketing is UTC — the
// accounting-period timezone is a known V1 simplification (see docs).
func (p PeriodType) PeriodID(pit time.Time) string {
	switch p {
	case PeriodTypeDaily:
		return pit.UTC().Format("2006-01-02")
	case PeriodTypeWeekly:
		isoYear, isoWeek := pit.UTC().ISOWeek()
		return fmt.Sprintf("%04d-W%02d", isoYear, isoWeek)
	case PeriodTypeMonthly:
		return pit.UTC().Format("2006-01")
	default:
		return ContinuousPeriod
	}
}

// Valid reports whether p is a recognised period type.
func (p PeriodType) Valid() bool {
	switch p {
	case PeriodTypeContinuous, PeriodTypeDaily, PeriodTypeWeekly, PeriodTypeMonthly:
		return true
	default:
		return false
	}
}

// ScheduleKind discriminates the Schedule shape. V1 beta ships on_demand only;
// cron lands at V1 GA. event_driven is V2.
type ScheduleKind string

const (
	ScheduleOnDemand ScheduleKind = "on_demand"
	ScheduleCron     ScheduleKind = "cron"
)

// Schedule controls when a rule is evaluated. Cron-specific fields are zero
// for on_demand schedules; consumers should branch on Kind. It marshals with
// the default encoder — plain string fields, no custom logic (the former
// SafetyMargin duration field was removed with the checkpoint flip, step 6b-2b).
type Schedule struct {
	Kind ScheduleKind `json:"kind"`
	Expr string       `json:"expr,omitempty"`
	TZ   string       `json:"tz,omitempty"`
}

// Rule is the customer-facing entity: a template + spec + schedule + delivery.
// The compiled CEL is persisted for explainability and to support post-GA raw
// expression mode without recompiling on every load.
type Rule struct {
	bun.BaseModel `bun:"reconciliations.rule" json:"-"`

	ID              uuid.UUID         `bun:",pk,nullzero"           json:"id"`
	ContractVersion ContractVersion   `bun:"-" json:"-"`
	Revision        string            `bun:"-" json:"-"`
	Name            string            `bun:",notnull"               json:"name"`
	TemplateKind    TemplateKind      `bun:"template_kind,notnull"  json:"templateKind"`
	TemplateSpec    json.RawMessage   `bun:"template_spec,type:jsonb,notnull" json:"templateSpec"`
	CompiledCEL     string            `bun:"compiled_cel,notnull"   json:"compiledCEL,omitempty"`
	Enabled         bool              `bun:",notnull"               json:"enabled"`
	Severity        Severity          `bun:",notnull"               json:"severity"`
	PeriodType      PeriodType        `bun:",notnull"               json:"periodType"`
	Schedule        *Schedule         `bun:",type:jsonb"            json:"schedule,omitempty"`
	Notifications   []string          `bun:",type:jsonb"            json:"notifications,omitempty"`
	Labels          map[string]string `bun:",type:jsonb"            json:"labels,omitempty"`
	CreatedAt       time.Time         `bun:"created_at,notnull,nullzero" json:"createdAt"`
	UpdatedAt       time.Time         `bun:"updated_at,notnull,nullzero" json:"updatedAt"`
}
