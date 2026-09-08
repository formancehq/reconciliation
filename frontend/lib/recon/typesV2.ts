import type {
  AckAlertRequest,
  AcceptAlertRequest,
  Actor,
  AlertEventType,
  AlertStatus,
  PeriodType,
  CursorResponse,
  Data,
  EvaluateRuleRequest,
  EvaluationResult,
  ResolveAlertRequest,
  RuleActivity,
  Schedule,
  Severity,
  SnoozeAlertRequest,
  Trigger,
  UnsnoozeAlertRequest,
  Verdict,
} from "./types"

export type ContractVersion = 1 | 2
export type TemplateKind =
  | "balance_equation"
  | "exchange_rate_bounds"
  | "source_consensus"
  | "coverage_ratio_bounds"
  | "stale_holds"
  | "balance_bounds"

export interface LedgerNamedSource {
  id: string
  label?: string
  kind?: "ledger"
  ledger: string
  query: Record<string, unknown>
  asset: string
}

export interface AccountMetadataNamedSource {
  id: string
  label?: string
  kind: "account_metadata"
  ledger: string
  query: Record<string, unknown>
  metadataKey: string
  asset: string
}

export type NamedSource = LedgerNamedSource | AccountMetadataNamedSource

export interface BalanceEquationTerm {
  source: string
  coefficient: number
}

export interface BalanceEquationSpec {
  sources: NamedSource[]
  terms: BalanceEquationTerm[]
  tolerance: string
}

export interface ExplicitRateBounds {
  min: string
  max: string
}

export interface TargetRateBounds {
  target: string
  toleranceBps: number
}

export type RateBounds = ExplicitRateBounds | TargetRateBounds

export interface ExchangeRateBoundsSpec {
  sources: NamedSource[]
  baseSource: string
  quoteSource: string
  rate: RateBounds
}

export interface SourceConsensusSpec {
  sources: NamedSource[]
  tolerance: string
}

export interface CoverageRatioBoundsSpec {
  sources: NamedSource[]
  numeratorTerms: BalanceEquationTerm[]
  denominatorTerms: BalanceEquationTerm[]
  ratio: RateBounds
}

/** One asset's inclusive limits in minor units. An empty side is unbounded, not zero. */
export interface BalanceBound {
  min?: string
  max?: string
}

export interface BalanceBoundsSpec {
  source: NamedSource
  /**
   * Inclusive limits keyed by asset — and the DECLARED asset universe. Unlike
   * every other V2 template these keys, not the assets the source holds, decide
   * what is checked: a floor must keep failing when a set drains to nothing.
   */
  bounds: Record<string, BalanceBound>
}

export type StaleHoldsMode = "stale" | "approaching"

/**
 * How a deadline is written on the account. `datetime` is a key the ledger
 * declares as a datetime; the `epoch_*` encodings are integer keys in the named
 * unit. Whichever it is, the key must be declared AND indexed on the ledger —
 * the service pushes the date comparison down to it.
 */
export type InstantEncoding =
  | "datetime"
  | "epoch_seconds"
  | "epoch_millis"
  | "epoch_micros"

export interface HoldDeadline {
  expiryKey?: string
  createdKey?: string
  encoding?: InstantEncoding
  maxAge?: string
}

export interface StaleHoldsSpec {
  source: LedgerNamedSource
  deadline: HoldDeadline
  mode?: StaleHoldsMode
  warnWithin?: string
  /** Per-rule read cap. Defaults to the engine budget server-side; never exceeds it. */
  maxHoldsScanned?: number
}

export type TemplateSpec =
  | BalanceEquationSpec
  | ExchangeRateBoundsSpec
  | SourceConsensusSpec
  | CoverageRatioBoundsSpec
  | StaleHoldsSpec
  | BalanceBoundsSpec

interface RuleCommon {
  id: string
  revision: string
  contractVersion: 2
  name: string
  compiledCEL?: string
  enabled: boolean
  severity: Severity
  periodType: PeriodType
  schedule?: Schedule
  notifications?: string[]
  labels?: Record<string, string>
  createdAt: string
  updatedAt: string
}

export type Rule =
  | (RuleCommon & {
      templateKind: "balance_equation"
      templateSpec: BalanceEquationSpec
    })
  | (RuleCommon & {
      templateKind: "exchange_rate_bounds"
      templateSpec: ExchangeRateBoundsSpec
    })
  | (RuleCommon & {
      templateKind: "source_consensus"
      templateSpec: SourceConsensusSpec
    })
  | (RuleCommon & {
      templateKind: "coverage_ratio_bounds"
      templateSpec: CoverageRatioBoundsSpec
    })
  | (RuleCommon & {
      templateKind: "stale_holds"
      templateSpec: StaleHoldsSpec
    })
  | (RuleCommon & {
      templateKind: "balance_bounds"
      templateSpec: BalanceBoundsSpec
    })

interface RuleRequestCommon {
  name: string
  severity?: Severity
  periodType?: PeriodType
  schedule?: Schedule
  notifications?: string[]
  labels?: Record<string, string>
  enabled?: boolean
}

export type RuleRequest =
  | (RuleRequestCommon & {
      templateKind: "balance_equation"
      templateSpec: BalanceEquationSpec
    })
  | (RuleRequestCommon & {
      templateKind: "exchange_rate_bounds"
      templateSpec: ExchangeRateBoundsSpec
    })
  | (RuleRequestCommon & {
      templateKind: "source_consensus"
      templateSpec: SourceConsensusSpec
    })
  | (RuleRequestCommon & {
      templateKind: "coverage_ratio_bounds"
      templateSpec: CoverageRatioBoundsSpec
    })
  | (RuleRequestCommon & {
      templateKind: "stale_holds"
      templateSpec: StaleHoldsSpec
    })
  | (RuleRequestCommon & {
      templateKind: "balance_bounds"
      templateSpec: BalanceBoundsSpec
    })

export interface RulePatchRequest {
  name?: string
  templateKind?: TemplateKind
  templateSpec?: TemplateSpec
  enabled?: boolean
  severity?: Severity
  schedule?: Schedule
  notifications?: string[]
  labels?: Record<string, string>
}

export interface EvidenceSource {
  id: string
  label?: string
  kind: "ledger" | "account_metadata"
  asset: string
  balance: string
  present: boolean
}

export interface BalanceEquationEvidenceSource extends EvidenceSource {
  coefficient: number
  contribution: string
}

export interface BalanceEquationEvidenceV2 {
  schemaVersion: 2
  operation: "balance_equation"
  asset: string
  sources: BalanceEquationEvidenceSource[]
  residual: string
  absoluteResidual: string
  tolerance: string
  compiledCEL: string
}

export interface Rational {
  numerator: string
  denominator: string
}

export interface EffectiveRateBounds {
  min: string
  max: string
}

export interface ExchangeRateBoundsEvidence {
  schemaVersion: 2
  operation: "exchange_rate_bounds"
  base: EvidenceSource
  quote: EvidenceSource
  observedRate?: Rational
  effectiveBounds: EffectiveRateBounds
  undefinedReason?: "base_balance_zero"
  compiledCEL: string
}

export interface SourceConsensusEvidenceV2 {
  schemaVersion: 2
  operation: "source_consensus"
  asset: string
  sources: EvidenceSource[]
  minimumSource: string
  minimumBalance: string
  maximumSource: string
  maximumBalance: string
  spread: string
  tolerance: string
  missingSources: string[]
  compiledCEL: string
}

export interface CoveragePortfolio {
  sources: BalanceEquationEvidenceSource[]
  total: string
}

export interface CoverageRatioBoundsEvidence {
  schemaVersion: 2
  operation: "coverage_ratio_bounds"
  asset: string
  numerator: CoveragePortfolio
  denominator: CoveragePortfolio
  observedRatio?: Rational
  effectiveBounds: EffectiveRateBounds
  undefinedReason?: "denominator_total_zero"
  compiledCEL: string
}

/**
 * One outcome per asset. It counts and totals the stale set rather than listing
 * it: `effectiveQuery` is the query this evaluation ran, so the set is
 * recoverable without every alert carrying a list that grows with the problem.
 */
export interface StaleHoldsEvidenceV2 {
  schemaVersion: 2
  operation: "stale_holds"
  mode: StaleHoldsMode
  asset: string
  sourceId: string
  ledger: string
  evaluatedAt: string
  deadlineOnOrBefore: string
  /** The band's lower bound — mode "approaching" only. */
  deadlineAfter?: string
  holdsMatched: number
  holdsBudget: number
  holdsReleased: number
  /**
   * Matched, funded, but rejected by the authoritative in-Go deadline check.
   * Normally 0 — the counts partition the matched set, so
   * `matched = released + rejected + flagged`, and a non-zero value means the
   * pushed-down predicate and the direct evaluation disagreed.
   */
  holdsRejected?: number
  holdsFlagged: number
  amountFlagged: string
  oldestDeadline?: string
  /**
   * The query the ledger answered, deadline cutoff included as a literal.
   * Re-running it does not reproduce this evaluation — there is no
   * point-in-time read, so the deadline half is frozen while balances stay
   * live.
   */
  effectiveQuery: string
  compiledCEL: string
}

export interface BalanceBoundsEvidenceV2 {
  schemaVersion: 2
  operation: "balance_bounds"
  asset: string
  source: EvidenceSource
  /** Only the sides the rule declared; an unbounded side is an absent key. */
  effectiveBounds: { min?: string; max?: string }
  /** Signed distance outside the limits: negative below the floor, positive above the ceiling. */
  excursion: string
  /** Present only on a failing outcome. */
  breachedBound?: "min" | "max"
  compiledCEL: string
}

export type Evidence =
  | BalanceEquationEvidenceV2
  | ExchangeRateBoundsEvidence
  | SourceConsensusEvidenceV2
  | CoverageRatioBoundsEvidence
  | StaleHoldsEvidenceV2
  | BalanceBoundsEvidenceV2

export interface Outcome {
  fingerprint: string
  passed: boolean
  evidence: Evidence
}

export interface Evaluation {
  id: string
  contractVersion: 2
  ruleID: string
  startedAt: string
  endedAt: string
  result: EvaluationResult
  evidence?: Outcome[] | Record<string, unknown>
  error?: string
  costUnits?: number
  createdAt: string
}

export interface Capture {
  transactionID: number
  contractVersion: 2
  ruleID: string
  periodID: string
  evaluationID: string
  templateKind: TemplateKind
  verdict: Verdict
  trigger: Trigger
  capturedAt: string
  ruleRevision?: string
  pit?: string
  startedAt?: string
  result?: EvaluationResult
  error?: string
  evidence?: Outcome[] | Record<string, unknown>
}

interface AckV2 {
  by: string
  at: string
  note?: string
  actor?: Actor
}
interface ResolutionV2 {
  kind: "auto" | "fixed_by_booking" | "accepted_by_business"
  by: string
  at: string
  note?: string
  transactionRefs?: string[]
  evidenceSnapshot?: Evidence
  expiresAt?: string
  actor?: Actor
}
interface SnoozeV2 {
  until: string
  by: string
  at: string
  note?: string
}

export interface Alert {
  id: string
  contractVersion: 2
  ruleID: string
  fingerprint: string
  periodID: string
  status: AlertStatus
  severity: Severity
  firstSeenAt: string
  lastSeenAt: string
  occurrenceCount: number
  lastEvaluationID: string
  evidence?: Evidence
  ack?: AckV2
  resolution?: ResolutionV2
  snooze?: SnoozeV2
  labels?: Record<string, string>
  createdAt: string
  updatedAt: string
}

/** V2 alert event — see AlertEvent (V1); /events is mounted for V1 and V2. */
export interface AlertEventV2 {
  id: string
  alertID: string
  evaluationID?: string | null
  type: AlertEventType
  prevStatus?: AlertStatus | null
  newStatus: AlertStatus
  payload?: Record<string, unknown>
  at: string
  isReopen: boolean
  notify: boolean
  /** Control-ledger transaction id of the write behind this event (see AlertEvent). */
  transactionId?: string
}

export type RuleResponse = Data<Rule>
export type RulesResponse = CursorResponse<Rule>
export type EvaluationResponse = Data<Evaluation>
export type CapturesResponse = CursorResponse<Capture>
export type RuleActivitiesResponse = CursorResponse<RuleActivity>
export type AlertResponse = Data<Alert>
export type AlertsResponse = CursorResponse<Alert>
export type AlertEventsResponse = CursorResponse<AlertEventV2>

export type {
  AckAlertRequest,
  AcceptAlertRequest,
  EvaluateRuleRequest,
  ResolveAlertRequest,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
}
