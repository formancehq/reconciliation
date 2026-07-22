package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/uptrace/bun"
)

// TemplateKind is the V1 public-API surface for rules — a typed catalog entry
// that compiles deterministically to an internal CEL expression. The kernel is
// not exposed at V1 GA; templates are the entire customer-facing surface.
// See ADR-001 for the rationale.
type TemplateKind string

const (
	// TemplateLedgerVsPoolDrift is the port of today's Policy behaviour:
	// compare a dynamic ledger account set against a dynamic payments pool.
	TemplateLedgerVsPoolDrift TemplateKind = "ledger_vs_pool_drift"
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

// Valid reports whether s is a recognised severity. The CHECK constraint in the
// migration mirrors this set, so validating at the API boundary turns a would-be
// 500 (DB constraint violation on bad input) into a 400.
func (s Severity) Valid() bool {
	switch s {
	case SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	default:
		return false
	}
}

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

// Valid reports whether k is a recognised schedule kind. The public schema only
// allows on_demand or cron; anything else (a typo, or a not-yet-shipped kind) is
// rejected at the boundary rather than persisted and silently ignored by the
// scheduler.
func (k ScheduleKind) Valid() bool {
	switch k {
	case ScheduleOnDemand, ScheduleCron:
		return true
	default:
		return false
	}
}

// Schedule controls when a rule is evaluated. Cron-specific fields are zero
// for on_demand schedules; consumers should branch on Kind.
//
// SafetyMargin is `time.Duration` in Go but transits the wire as a Go duration
// string ("30s", "1m"), matching the OpenAPI contract. Custom Marshal/Unmarshal
// methods handle the translation; `json:"-"` keeps the default encoder from
// leaking nanoseconds into the JSON output.
type Schedule struct {
	Kind                    ScheduleKind  `json:"kind"`
	Expr                    string        `json:"expr,omitempty"`
	TZ                      string        `json:"tz,omitempty"`
	SafetyMargin            time.Duration `json:"-"`
	SafetyMarginWasProvided bool          `json:"-"`
}

// scheduleWire is the on-the-wire representation: SafetyMargin is a string in
// Go-duration format. Kept private — callers see the Schedule struct.
type scheduleWire struct {
	Kind         ScheduleKind `json:"kind"`
	Expr         string       `json:"expr,omitempty"`
	TZ           string       `json:"tz,omitempty"`
	SafetyMargin *string      `json:"safetyMargin,omitempty"`
}

func (s Schedule) MarshalJSON() ([]byte, error) {
	w := scheduleWire{
		Kind: s.Kind,
		Expr: s.Expr,
		TZ:   s.TZ,
	}
	if s.SafetyMarginWasProvided || s.SafetyMargin != 0 {
		margin := s.SafetyMargin.String()
		w.SafetyMargin = &margin
	}
	return json.Marshal(w)
}

func (s *Schedule) UnmarshalJSON(data []byte) error {
	var w scheduleWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	s.Kind = w.Kind
	s.Expr = w.Expr
	s.TZ = w.TZ
	if w.SafetyMargin == nil {
		s.SafetyMargin = 0
		s.SafetyMarginWasProvided = false
		return nil
	}
	d, err := time.ParseDuration(*w.SafetyMargin)
	if err != nil {
		return fmt.Errorf("schedule.safetyMargin: %w", err)
	}
	s.SafetyMargin = d
	s.SafetyMarginWasProvided = true
	return nil
}

// Next returns the next occurrence strictly after the supplied instant. Cron
// schedules are evaluated in their declared timezone (UTC by default), while
// on-demand schedules have no next occurrence.
func (s *Schedule) Next(after time.Time) (time.Time, error) {
	if s == nil || s.Kind != ScheduleCron || s.Expr == "" {
		return time.Time{}, nil
	}
	tz := s.TZ
	if tz == "" {
		tz = "UTC"
	}
	parsed, err := cron.ParseStandard(fmt.Sprintf("CRON_TZ=%s %s", tz, s.Expr))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q (%s): %w", s.Expr, tz, err)
	}
	return parsed.Next(after).UTC(), nil
}

// Rule is the customer-facing entity: a template + spec + schedule + delivery.
// TemplateSpec is the executable source of truth. ExplanationCEL is a
// representative, human-readable expression and is never loaded for runtime
// evaluation.
type Rule struct {
	bun.BaseModel `bun:"reconciliations.rule" json:"-"`

	ID             uuid.UUID         `bun:",pk,nullzero"           json:"id"`
	Name           string            `bun:",notnull"               json:"name"`
	TemplateKind   TemplateKind      `bun:"template_kind,notnull"  json:"templateKind"`
	TemplateSpec   json.RawMessage   `bun:"template_spec,type:jsonb,notnull" json:"templateSpec"`
	ExplanationCEL string            `bun:"explanation_cel,notnull" json:"explanationCEL,omitempty"`
	Enabled        bool              `bun:",notnull"               json:"enabled"`
	Severity       Severity          `bun:",notnull"               json:"severity"`
	Cadence        Cadence           `bun:",notnull"               json:"cadence"`
	Schedule       *Schedule         `bun:",type:jsonb"            json:"schedule,omitempty"`
	Notifications  []string          `bun:",type:jsonb"            json:"notifications,omitempty"`
	Labels         map[string]string `bun:",type:jsonb"            json:"labels,omitempty"`
	// Revision is incremented for every material rule update. Scheduled jobs
	// capture it and are fenced at commit time so work computed from an old rule
	// definition can never overwrite results from the current one.
	Revision  int64      `bun:",notnull"               json:"-"`
	NextRunAt *time.Time `bun:"next_run_at,nullzero"   json:"-"`
	CreatedAt time.Time  `bun:"created_at,notnull,nullzero" json:"createdAt"`
	UpdatedAt time.Time  `bun:"updated_at,notnull,nullzero" json:"updatedAt"`
}
