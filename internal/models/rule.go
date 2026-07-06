package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// TemplateKind is the V1 public-API surface for rules — a typed catalog entry
// that compiles deterministically to an internal CEL expression. The kernel is
// not exposed at V1 GA; templates are the entire customer-facing surface.
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

// Cadence is the reconciliation rhythm of a rule — it decides how a failing
// fingerprint is scoped into a period. A fresh alert is opened per period, so a
// March break and an April break of the same fingerprint are distinct,
// independently-closable cases; resolving April never rewrites March's record.
// See docs/technical/alert-period-model.md.
type Cadence string

const (
	// CadenceContinuous is live monitoring: a single unbounded period. A
	// failing fingerprint reopens in place until resolved, like a classic
	// monitoring alert. Default — periodic scoping is opt-in per rule.
	CadenceContinuous Cadence = "continuous"
	// CadenceDaily buckets cases by UTC calendar day (period id "2006-01-02").
	CadenceDaily Cadence = "daily"
	// CadenceWeekly buckets cases by ISO week (period id "2026-W12"). The ISO
	// year can differ from the calendar year near year boundaries — ISOWeek()
	// returns the correct ISO year, so the bucket is unambiguous.
	CadenceWeekly Cadence = "weekly"
	// CadenceMonthly buckets cases by UTC calendar month (period id "2006-01").
	CadenceMonthly Cadence = "monthly"
)

// ContinuousPeriod is the sentinel period id for CadenceContinuous and the safe
// fallback for an unset/unknown cadence: one unbounded scope, which reproduces
// the original (rule_id, fingerprint) dedup exactly.
const ContinuousPeriod = "continuous"

// PeriodID maps an evaluation's point-in-time to the period a failing
// fingerprint belongs to under this cadence. Deterministic: any instant in the
// same bucket yields the same id, so re-evaluating a period continues its
// existing case rather than spawning a new one. Bucketing is UTC — the
// accounting-period timezone is a known V1 simplification (see docs).
func (c Cadence) PeriodID(pit time.Time) string {
	switch c {
	case CadenceDaily:
		return pit.UTC().Format("2006-01-02")
	case CadenceWeekly:
		isoYear, isoWeek := pit.UTC().ISOWeek()
		return fmt.Sprintf("%04d-W%02d", isoYear, isoWeek)
	case CadenceMonthly:
		return pit.UTC().Format("2006-01")
	default:
		return ContinuousPeriod
	}
}

// Valid reports whether c is a recognised cadence.
func (c Cadence) Valid() bool {
	switch c {
	case CadenceContinuous, CadenceDaily, CadenceWeekly, CadenceMonthly:
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

	ID            uuid.UUID         `bun:",pk,nullzero"           json:"id"`
	Name          string            `bun:",notnull"               json:"name"`
	TemplateKind  TemplateKind      `bun:"template_kind,notnull"  json:"templateKind"`
	TemplateSpec  json.RawMessage   `bun:"template_spec,type:jsonb,notnull" json:"templateSpec"`
	CompiledCEL   string            `bun:"compiled_cel,notnull"   json:"compiledCEL,omitempty"`
	Enabled       bool              `bun:",notnull"               json:"enabled"`
	Severity      Severity          `bun:",notnull"               json:"severity"`
	Cadence       Cadence           `bun:",notnull"               json:"cadence"`
	Schedule      *Schedule         `bun:",type:jsonb"            json:"schedule,omitempty"`
	Notifications []string          `bun:",type:jsonb"            json:"notifications,omitempty"`
	Labels        map[string]string `bun:",type:jsonb"            json:"labels,omitempty"`
	CreatedAt     time.Time         `bun:"created_at,notnull,nullzero" json:"createdAt"`
	UpdatedAt     time.Time         `bun:"updated_at,notnull,nullzero" json:"updatedAt"`
}
