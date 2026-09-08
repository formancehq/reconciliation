package templates

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// stale_holds is the catalog's only time-based control: it flags held funds
// whose deadline has passed (mode "stale") or is about to (mode
// "approaching"). A "hold" is one ledger account carrying a non-zero balance
// plus a deadline in its metadata — either an expiry recorded on the hold,
// or a creation instant to which maxAge is added.
//
// Two properties are worth understanding before reading the code.
//
//  1. The time predicate is pushed into the ledger query. Ledger V3 has no
//     point-in-time read (ADR-003: every source reads live), so age cannot be
//     derived by comparing "now" against "then". It is read from state instead,
//     and the comparison is done by the ledger's own metadata index: the
//     evaluation clock is materialised into an integer cutoff and appended to
//     the rule's query as a `$lte` clause (plus a `$gt` lower bound in
//     `approaching` mode). The ledger returns only the holds already past (or
//     approaching) their deadline, so the read scales with the number of
//     *stale* holds rather than the number of holds.
//
//  2. The kernel stays time-free. The clock enters as a literal, never as a
//     `now()` builtin, so the `compiledCEL` recorded in evidence is an exact,
//     re-runnable record of the predicate this evaluation applied — a builtin
//     reading the wall clock would silently re-clock on replay. The rendered
//     CEL uses only ledgerSet/balance.
//
// The outcome is one aggregate per asset: a count, a total, and the query that
// found them. It names no individual hold, deliberately. A rule selects the set
// it watches (an address prefix plus whatever metadata narrows it to one desk or
// book), so which holds are stale is a question for that query — which evidence
// carries verbatim — rather than a list that grows with the size of the problem
// inside every alert.
//
// See docs/technical/stale-holds.md for the design and its open questions.

// StaleHoldsMode selects which side of the deadline the rule watches. The two
// modes are the same predicate at different cutoffs, so an "warn early, page
// late" setup is two rules — severity is declared per rule.
type StaleHoldsMode string

const (
	// StaleHoldsBreached flags holds whose deadline has passed: deadline <= now.
	StaleHoldsBreached StaleHoldsMode = "stale"
	// StaleHoldsApproaching flags holds inside the warning band:
	// now < deadline <= now + warnWithin. It is a *band*, not a threshold, so a
	// hold crossing into "stale" leaves this rule's outcomes and its warning
	// alert auto-resolves as the stale rule's alert opens.
	StaleHoldsApproaching StaleHoldsMode = "approaching"
)

// InstantEncoding declares how a deadline metadata value is written, because
// the ledger's own type decides both how it reads back and how it compares:
//
//   - "datetime": the key is declared datetime in the ledger. It is stored as
//     int64 epoch micros (so the query cutoff is micros) but reads back as an
//     RFC3339Nano string, which is why metadataInt cannot be used here.
//   - "epoch_*": the key is declared as an integer holding an epoch in the
//     named unit; it queries and reads back in that unit.
//
// Nanosecond epochs are deliberately absent: query values travel as JSON
// numbers and are rejected past 2^53, which epoch nanos exceed. Micros stay
// inside that range for another two centuries.
type InstantEncoding string

const (
	EncodingDatetime     InstantEncoding = "datetime"
	EncodingEpochSeconds InstantEncoding = "epoch_seconds"
	EncodingEpochMillis  InstantEncoding = "epoch_millis"
	EncodingEpochMicros  InstantEncoding = "epoch_micros"
)

// HoldDeadlineSpec says where a hold's deadline comes from. When both keys are
// declared, the recorded expiry wins for any hold that carries one and createdKey
// + maxAge is the fallback for the rest — expressed as a single
// `$or` so the ledger still does the filtering.
type HoldDeadlineSpec struct {
	ExpiryKey  string          `json:"expiryKey,omitempty"`
	CreatedKey string          `json:"createdKey,omitempty"`
	Encoding   InstantEncoding `json:"encoding,omitempty"`
	MaxAge     string          `json:"maxAge,omitempty"`
}

// StaleHoldsSpec is the typed spec for stale_holds.
type StaleHoldsSpec struct {
	Source     V2NamedSource    `json:"source"`
	Deadline   HoldDeadlineSpec `json:"deadline"`
	Mode       StaleHoldsMode   `json:"mode,omitempty"`
	WarnWithin string           `json:"warnWithin,omitempty"`

	// MaxHoldsScanned caps how many hold accounts a single evaluation may read,
	// below the engine-wide accounts budget. It bounds the *read* — the matched
	// set is what the deadline filter returns — and the outcome is one aggregate
	// per asset regardless of how large that set is. Exceeding the cap fails the
	// evaluation rather than truncating it: a truncated read would understate the
	// count and the total, reporting a smaller problem than the one that exists.
	// It can only ever lower the engine's budget, never raise it: that limit
	// protects the ledger from any one evaluation and is not a rule author's to
	// relax.
	MaxHoldsScanned *int `json:"maxHoldsScanned,omitempty"`
}

type StaleHolds struct{}

func NewStaleHolds() *StaleHolds { return &StaleHolds{} }

func (*StaleHolds) Kind() models.TemplateKind { return models.TemplateStaleHolds }

// normalize applies the spec defaults in place.
func (s *StaleHoldsSpec) normalize() {
	if s.Mode == "" {
		s.Mode = StaleHoldsBreached
	}
	if s.Deadline.Encoding == "" {
		s.Deadline.Encoding = EncodingDatetime
	}
}

// holdsBudget is the accounts budget this evaluation reads under: the engine's
// limit unless the rule names a smaller one. An explicit maxHoldsScanned never
// gets past the engine's own limit — a rule may tighten its blast radius, and
// may not loosen the bound that protects the ledger from every evaluation.
func (spec *StaleHoldsSpec) holdsBudget(engineBudget int) int {
	if spec.MaxHoldsScanned != nil {
		return min(*spec.MaxHoldsScanned, engineBudget)
	}
	return engineBudget
}

// maxAge returns the parsed fallback age. Zero when createdKey is unused.
func (s *StaleHoldsSpec) maxAge() (time.Duration, error) {
	if s.Deadline.CreatedKey == "" {
		return 0, nil
	}
	return parsePositiveDuration(s.Deadline.MaxAge, "deadline.maxAge")
}

func (t *StaleHolds) Validate(raw json.RawMessage) error {
	var spec StaleHoldsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	spec.normalize()

	if spec.Source.Kind == SourceAccountMetadata {
		return fmt.Errorf("%w: stale_holds reads held balances, so its source must be a ledger source (field: source.kind)", ErrInvalidSpec)
	}
	if err := spec.Source.validateAt("source"); err != nil {
		return err
	}
	// Per-asset fan-out is not wired through this template's hold scan yet: a
	// hold's amount, deadline and identity are per (account, asset), so the
	// wildcard needs its own outcome shape rather than the shared one.
	if spec.Source.wildcard() {
		return fmt.Errorf("%w: stale_holds needs a named asset — asset %q is not supported here yet (field: source.asset)",
			ErrInvalidSpec, AssetWildcard)
	}

	switch spec.Deadline.Encoding {
	case EncodingDatetime, EncodingEpochSeconds, EncodingEpochMillis, EncodingEpochMicros:
	default:
		return fmt.Errorf("%w: deadline.encoding must be one of %q, %q, %q, %q (got %q)",
			ErrInvalidSpec, EncodingDatetime, EncodingEpochSeconds, EncodingEpochMillis, EncodingEpochMicros, spec.Deadline.Encoding)
	}
	if spec.Deadline.ExpiryKey == "" && spec.Deadline.CreatedKey == "" {
		return fmt.Errorf("%w: deadline must set expiryKey, createdKey, or both (fields: deadline.expiryKey, deadline.createdKey)", ErrInvalidSpec)
	}
	if spec.Deadline.CreatedKey == "" && spec.Deadline.MaxAge != "" {
		return fmt.Errorf("%w: deadline.maxAge applies to deadline.createdKey, which is not set — an expiry from deadline.expiryKey is used as-is (field: deadline.maxAge)", ErrInvalidSpec)
	}
	if _, err := spec.maxAge(); err != nil {
		return err
	}

	switch spec.Mode {
	case StaleHoldsBreached:
		if spec.WarnWithin != "" {
			return fmt.Errorf("%w: warnWithin applies to mode %q; mode %q flags holds whose deadline has already passed (field: warnWithin)",
				ErrInvalidSpec, StaleHoldsApproaching, StaleHoldsBreached)
		}
	case StaleHoldsApproaching:
		if _, err := parsePositiveDuration(spec.WarnWithin, "warnWithin"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: mode must be %q or %q (got %q)", ErrInvalidSpec, StaleHoldsBreached, StaleHoldsApproaching, spec.Mode)
	}

	if spec.MaxHoldsScanned != nil && *spec.MaxHoldsScanned <= 0 {
		return fmt.Errorf("%w: maxHoldsScanned must be positive (got %d)", ErrInvalidSpec, *spec.MaxHoldsScanned)
	}
	return nil
}

// Queries returns the *augmented* source — the rule's own query joined with the
// deadline clause — so rule-create validation probes the metadata keys the
// evaluation will actually filter on. A deadline key with no ready,
// type-compatible accounts index is then rejected as 400 VALIDATION instead of
// erroring at evaluation. The cutoff is a placeholder (epoch zero): the ledger
// validates the query *plan*, not the values.
func (t *StaleHolds) Queries(raw json.RawMessage) ([]SourceSpec, error) {
	spec, err := parseStaleHoldsSpec(raw)
	if err != nil {
		return nil, err
	}
	query, err := spec.effectiveQuery(spec.explainWindow())
	if err != nil {
		return nil, err
	}
	return []SourceSpec{{Ledger: spec.Source.Ledger, Query: query}}, nil
}

// Explain renders the representative CEL. The deadline cutoff is materialised
// per evaluation, so the persisted compiled_cel shows the shape with an
// epoch-zero cutoff; each evaluation's evidence.compiledCEL carries the real
// instant it used.
func (t *StaleHolds) Explain(raw json.RawMessage) (string, error) {
	spec, err := parseStaleHoldsSpec(raw)
	if err != nil {
		return "", err
	}
	query, err := spec.effectiveQuery(spec.explainWindow())
	if err != nil {
		return "", err
	}
	return staleHoldsCEL(spec.Source.Ledger, query, spec.Source.Asset), nil
}

func (t *StaleHolds) Evaluate(
	ctx context.Context,
	raw json.RawMessage,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	in engine.EvalInput,
) ([]Outcome, error) {
	spec, err := parseStaleHoldsSpec(raw)
	if err != nil {
		return nil, err
	}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return nil, err
	}
	if in.PIT.IsZero() {
		return nil, fmt.Errorf("stale_holds: evaluation PIT is required — the deadline predicate is evaluated against it")
	}

	now := in.PIT.UTC()
	window, err := spec.window(now)
	if err != nil {
		return nil, err
	}
	// The same path Explain and Queries render, so the query an evaluation runs
	// and the query the create-time probe validates cannot drift apart.
	query, err := spec.effectiveQuery(window)
	if err != nil {
		return nil, err
	}

	src := SourceSpec{Ledger: spec.Source.Ledger, Query: query}
	budget := spec.holdsBudget(eng.MaxAccountsScanned())
	accounts, err := src.resolveAccounts(ctx, resolvers, budget)
	if err != nil {
		return nil, fmt.Errorf("scout holds on %s: %w", src.label(), err)
	}

	asset := spec.Source.Asset
	scan := scanSummary{
		matched: len(accounts),
		total:   new(big.Int),
		now:     now,
		window:  window,
		budget:  budget,
	}
	for _, account := range accounts {
		amount := zeroIfNil(account.Balances[asset])
		// A released hold keeps its account row and its deadline metadata — only
		// the zeroed volume is evicted — so it still matches a deadline filter.
		// Balances are not filterable in a query, so this is post-filtered here.
		if amount.Sign() == 0 {
			scan.released++
			continue
		}
		deadline, err := spec.holdDeadline(account)
		if err != nil {
			return nil, err
		}
		// The query already narrowed the set; this check is authoritative. Direct
		// evaluation decides the outcome, the pushdown is what makes it cheap.
		//
		// Counted rather than dropped. The two are equivalent by construction —
		// `created <= until - maxAge` is the same statement as
		// `created + maxAge <= until` — so this branch should not fire, and
		// silently skipping it let `matched = released + flagged` stop holding
		// with nothing to say where the rows went. One benign cause exists: a
		// deadline key present but blank satisfies the query's `$exists` and is
		// treated as absent here, so it falls to the createdKey basis and can
		// land outside the window.
		if !window.contains(deadline) {
			scan.rejected++
			continue
		}
		// Accumulated, not collected. The outcome is one aggregate per asset, so
		// retaining every flagged hold would grow the evaluation's memory with the
		// size of the problem to report a count and a sum.
		scan.flagged++
		scan.total.Add(scan.total, amount)
		if scan.oldest.IsZero() || deadline.Before(scan.oldest) {
			scan.oldest = deadline
		}
	}

	return spec.aggregateOutcome(scan, query), nil
}

// scanSummary is what one evaluation observed. The flagged holds are counted
// and summed as they are seen rather than retained: nothing downstream reports
// them individually.
type scanSummary struct {
	matched  int
	released int
	rejected int
	flagged  int
	total    *big.Int
	oldest   time.Time
	now      time.Time
	window   deadlineWindow
	budget   int
}

// aggregateOutcome emits one outcome per asset: the count, the total trapped,
// and the query that found them.
func (spec *StaleHoldsSpec) aggregateOutcome(scan scanSummary, query json.RawMessage) []Outcome {
	return []Outcome{{
		Fingerprint: fingerprintFor("asset", spec.Source.Asset),
		Passed:      scan.flagged == 0,
		Evidence:    spec.summaryEvidence(scan, query),
	}}
}

// summaryEvidence describes the scan without naming a single hold. An operator
// investigating a break needs the set, not a prefix of it, and the set is
// recoverable: effectiveQuery is the exact query this evaluation ran, deadline
// cutoff included as an integer literal.
//
// The counts partition what the ledger returned:
//
//	holdsMatched = holdsReleased + holdsRejected + holdsFlagged
//
// holdsRejected is normally zero — a non-zero value means the pushed-down
// predicate and the authoritative in-Go check disagreed about a hold, which is
// worth seeing rather than losing.
//
// It is in this module's own query dialect — the same shape a rule's
// source.query takes — so it drops straight back into a rule. It is NOT
// byte-compatible with the ledger's HTTP `?filter=` dialect, which spells
// existence `{"$exists":{"metadata":"k"}}` where this emits
// `{"$exists":{"metadata[k]":true}}`; a rule declaring both deadline keys
// therefore needs that one clause translated to run there directly.
//
// Re-running it does not reconstruct this evaluation either way. Ledger V3 has
// no point-in-time read (ADR-003), so the deadline half of the predicate is
// frozen while balances stay live: the answer later is "the holds still past
// this cutoff that are still funded", which is the useful question anyway.
func (spec *StaleHoldsSpec) summaryEvidence(scan scanSummary, query json.RawMessage) map[string]any {
	evidence := map[string]any{
		"schemaVersion":      2,
		"operation":          "stale_holds",
		"mode":               string(spec.Mode),
		"asset":              spec.Source.Asset,
		"sourceId":           spec.Source.ID,
		"ledger":             spec.Source.Ledger,
		"evaluatedAt":        scan.now.Format(time.RFC3339),
		"deadlineOnOrBefore": scan.window.until.Format(time.RFC3339),
		"holdsMatched":       scan.matched,
		"holdsBudget":        scan.budget,
		"holdsReleased":      scan.released,
		"holdsRejected":      scan.rejected,
		"holdsFlagged":       scan.flagged,
		"amountFlagged":      scan.total.String(),
		"effectiveQuery":     string(query),
		"compiledCEL":        staleHoldsCEL(spec.Source.Ledger, query, spec.Source.Asset),
	}
	if scan.window.after != nil {
		evidence["deadlineAfter"] = scan.window.after.Format(time.RFC3339)
	}
	if !scan.oldest.IsZero() {
		evidence["oldestDeadline"] = scan.oldest.Format(time.RFC3339)
	}
	return evidence
}

// --- deadlines ---------------------------------------------------------------

// deadlineWindow is the interval of deadlines this evaluation selects: `until`
// inclusive, `after` (when set) exclusive. Mode "stale" leaves `after` unset —
// any deadline at or before now qualifies, however old. Mode "approaching"
// sets both, making the warning a band that a hold leaves as it goes stale.
type deadlineWindow struct {
	after *time.Time
	until time.Time
}

func (w deadlineWindow) contains(deadline time.Time) bool {
	if w.after != nil && !deadline.After(*w.after) {
		return false
	}
	return !deadline.After(w.until)
}

// shift moves the window by -d, converting a window over deadlines into the
// equivalent window over creation instants (deadline = created + maxAge).
func (w deadlineWindow) shift(d time.Duration) deadlineWindow {
	shifted := deadlineWindow{until: w.until.Add(-d)}
	if w.after != nil {
		after := w.after.Add(-d)
		shifted.after = &after
	}
	return shifted
}

func (spec *StaleHoldsSpec) window(now time.Time) (deadlineWindow, error) {
	if spec.Mode == StaleHoldsApproaching {
		warn, err := parsePositiveDuration(spec.WarnWithin, "warnWithin")
		if err != nil {
			return deadlineWindow{}, err
		}
		lower := now
		return deadlineWindow{after: &lower, until: now.Add(warn)}, nil
	}
	return deadlineWindow{until: now}, nil
}

// explainWindow is the deterministic placeholder window used by Explain and
// Queries, which have no evaluation clock. Anchored at epoch zero so the
// rendered CEL and the create-time probe are stable across calls.
func (spec *StaleHoldsSpec) explainWindow() deadlineWindow {
	epoch := time.Unix(0, 0).UTC()
	window, err := spec.window(epoch)
	if err != nil {
		// warnWithin is validated at rule create; fall back to the bare cutoff
		// rather than failing to describe the rule.
		return deadlineWindow{until: epoch}
	}
	return window
}

// holdDeadline resolves one hold's deadline: the recorded expiry when the hold
// carries one, else creation + maxAge. A hold matched by the query but missing
// or misencoding its deadline is an error, not a silent skip — the same stance
// SumAccountMetadataInt takes on a metadata balance that didn't populate.
func (spec *StaleHoldsSpec) holdDeadline(account engine.Account) (time.Time, error) {
	if spec.Deadline.ExpiryKey != "" {
		if raw, ok := account.Metadata[spec.Deadline.ExpiryKey]; ok && strings.TrimSpace(raw) != "" {
			deadline, err := spec.Deadline.Encoding.parse(raw)
			if err != nil {
				return time.Time{}, fmt.Errorf("account %q metadata[%s]: %w", account.Address, spec.Deadline.ExpiryKey, err)
			}
			return deadline, nil
		}
	}
	if spec.Deadline.CreatedKey != "" {
		raw, ok := account.Metadata[spec.Deadline.CreatedKey]
		if !ok || strings.TrimSpace(raw) == "" {
			return time.Time{}, fmt.Errorf("account %q has no metadata[%s] to date the hold from", account.Address, spec.Deadline.CreatedKey)
		}
		created, err := spec.Deadline.Encoding.parse(raw)
		if err != nil {
			return time.Time{}, fmt.Errorf("account %q metadata[%s]: %w", account.Address, spec.Deadline.CreatedKey, err)
		}
		maxAge, err := spec.maxAge()
		if err != nil {
			return time.Time{}, err
		}
		return created.Add(maxAge), nil
	}
	return time.Time{}, fmt.Errorf("account %q has no metadata[%s] and the rule declares no fallback", account.Address, spec.Deadline.ExpiryKey)
}

// encode renders an instant in the unit the metadata key is compared in. A
// datetime-typed key is stored by the ledger as int64 epoch micros, so it
// shares the micros encoding.
func (e InstantEncoding) encode(t time.Time) int64 {
	switch e {
	case EncodingEpochSeconds:
		return t.Unix()
	case EncodingEpochMillis:
		return t.UnixMilli()
	default:
		return t.UnixMicro()
	}
}

// parse reads a deadline back out of account metadata. A datetime-typed value
// reaches us as RFC3339Nano (the ledger renders it that way, which is why
// metadataInt cannot read it); the epoch encodings arrive as base-10 integers.
func (e InstantEncoding) parse(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if e == EncodingDatetime {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}, fmt.Errorf("%q is not an RFC3339 datetime (encoding %q)", raw, e)
		}
		return parsed.UTC(), nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a base-10 integer (encoding %q)", raw, e)
	}
	switch e {
	case EncodingEpochSeconds:
		return time.Unix(n, 0).UTC(), nil
	case EncodingEpochMillis:
		return time.UnixMilli(n).UTC(), nil
	default:
		return time.UnixMicro(n).UTC(), nil
	}
}

// --- query construction ------------------------------------------------------

// effectiveQuery joins the rule's own hold query with the deadline clause for
// the given window, so the ledger returns only the holds this evaluation cares
// about.
func (spec *StaleHoldsSpec) effectiveQuery(window deadlineWindow) (json.RawMessage, error) {
	clause, err := spec.deadlineClause(window)
	if err != nil {
		return nil, err
	}
	return spec.queryWith(clause)
}

// queryWith joins the rule's own hold query with an already-rendered deadline
// clause.
func (spec *StaleHoldsSpec) queryWith(clause json.RawMessage) (json.RawMessage, error) {
	base, err := compactJSON(spec.Source.Query)
	if err != nil {
		return nil, fmt.Errorf("%w: source.query is not valid JSON: %v", ErrInvalidSpec, err)
	}
	return andQuery(base, clause), nil
}

// deadlineClause renders the metadata predicate selecting the window.
//
//   - expiry only:  the window over metadata[expiryKey]
//   - created only: the window shifted back by maxAge over metadata[createdKey]
//   - both:         expiry when the hold carries one, else the shifted created
//     window — an `$or` over `$exists`, so the ledger still filters
func (spec *StaleHoldsSpec) deadlineClause(window deadlineWindow) (json.RawMessage, error) {
	encoding := spec.Deadline.Encoding
	maxAge, err := spec.maxAge()
	if err != nil {
		return nil, err
	}

	switch {
	case spec.Deadline.CreatedKey == "":
		return andQuery(rangeClauses(spec.Deadline.ExpiryKey, window, encoding)...), nil
	case spec.Deadline.ExpiryKey == "":
		return andQuery(rangeClauses(spec.Deadline.CreatedKey, window.shift(maxAge), encoding)...), nil
	default:
		withExpiry := andQuery(append(
			[]json.RawMessage{existsClause(spec.Deadline.ExpiryKey, true)},
			rangeClauses(spec.Deadline.ExpiryKey, window, encoding)...,
		)...)
		withoutExpiry := andQuery(append(
			[]json.RawMessage{existsClause(spec.Deadline.ExpiryKey, false)},
			rangeClauses(spec.Deadline.CreatedKey, window.shift(maxAge), encoding)...,
		)...)
		return json.RawMessage(`{"$or":[` + string(withExpiry) + `,` + string(withoutExpiry) + `]}`), nil
	}
}

// rangeClauses renders `metadata[key] <= until` (plus `> after` when the window
// is a band) in the key's own encoding. Returned as separate fragments so the
// caller joins them into one flat `$and` rather than nesting.
func rangeClauses(key string, window deadlineWindow, encoding InstantEncoding) []json.RawMessage {
	clauses := make([]json.RawMessage, 0, 2)
	if window.after != nil {
		clauses = append(clauses, comparisonClause("$gt", key, encoding.encode(*window.after)))
	}
	return append(clauses, comparisonClause("$lte", key, encoding.encode(window.until)))
}

func comparisonClause(op, key string, value int64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{%s:{%s:%s}}`,
		celString(op), celString("metadata["+key+"]"), strconv.FormatInt(value, 10)))
}

func existsClause(key string, present bool) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"$exists":{%s:%t}}`, celString("metadata["+key+"]"), present))
}

// andQuery joins query fragments under `$and`, dropping empty ones. A single
// surviving fragment is returned as-is so the rendered query stays readable.
func andQuery(parts ...json.RawMessage) json.RawMessage {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if hasMeaningfulJSON(part) {
			kept = append(kept, string(part))
		}
	}
	switch len(kept) {
	case 0:
		return json.RawMessage(`{}`)
	case 1:
		return json.RawMessage(kept[0])
	default:
		return json.RawMessage(`{"$and":[` + strings.Join(kept, ",") + `]}`)
	}
}

// compactJSON strips insignificant whitespace so a spec's own formatting can't
// make the rendered query — and therefore the persisted CEL — unstable.
func compactJSON(raw json.RawMessage) (json.RawMessage, error) {
	if !hasMeaningfulJSON(raw) {
		return nil, nil
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return nil, err
	}
	return json.RawMessage(out.Bytes()), nil
}

// staleHoldsCEL renders the kernel form of the check: no funds remain in the
// holds the query selected. The deadline lives inside the query as a literal
// cutoff, so the expression is complete and re-runnable as written.
func staleHoldsCEL(ledger string, query json.RawMessage, asset string) string {
	return fmt.Sprintf("balance(ledgerSet(%s, %s), %s) == 0", celString(ledger), celJSON(query), celString(asset))
}

// parseStaleHoldsSpec unmarshals, defaults and validates a spec in one step —
// Evaluate, Explain and Queries all need a spec they can trust.
func parseStaleHoldsSpec(raw json.RawMessage) (*StaleHoldsSpec, error) {
	var spec StaleHoldsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if err := (&StaleHolds{}).Validate(raw); err != nil {
		return nil, err
	}
	spec.normalize()
	return &spec, nil
}

// parsePositiveDuration parses a Go duration string ("48h", "90m") and requires
// it to be positive.
func parsePositiveDuration(value, field string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("%w: %s is required", ErrInvalidSpec, field)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a duration such as \"48h\" or \"90m\" (got %q)", ErrInvalidSpec, field, value)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%w: %s must be positive (got %q)", ErrInvalidSpec, field, value)
	}
	return parsed, nil
}
