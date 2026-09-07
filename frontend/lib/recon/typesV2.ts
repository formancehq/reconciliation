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
export type TemplateKindV2 =
  | "balance_equation"
  | "exchange_rate_bounds"
  | "source_consensus"
  | "coverage_ratio_bounds"
  | "stale_holds"

export interface LedgerNamedSourceV2 {
  id: string
  label?: string
  kind?: "ledger"
  ledger: string
  query: Record<string, unknown>
  asset: string
}

export interface AccountMetadataNamedSourceV2 {
  id: string
  label?: string
  kind: "account_metadata"
  ledger: string
  query: Record<string, unknown>
  metadataKey: string
  asset: string
}

export type NamedSourceV2 = LedgerNamedSourceV2 | AccountMetadataNamedSourceV2

export interface BalanceEquationTermV2 {
  source: string
  coefficient: number
}

export interface BalanceEquationSpecV2 {
  sources: NamedSourceV2[]
  terms: BalanceEquationTermV2[]
  tolerance: string
}

export interface ExplicitRateBoundsV2 {
  min: string
  max: string
}

export interface TargetRateBoundsV2 {
  target: string
  toleranceBps: number
}

export type RateBoundsV2 = ExplicitRateBoundsV2 | TargetRateBoundsV2

export interface ExchangeRateBoundsSpecV2 {
  sources: NamedSourceV2[]
  baseSource: string
  quoteSource: string
  rate: RateBoundsV2
}

export interface SourceConsensusSpecV2 {
  sources: NamedSourceV2[]
  tolerance: string
}

export interface CoverageRatioBoundsSpecV2 {
  sources: NamedSourceV2[]
  numeratorTerms: BalanceEquationTermV2[]
  denominatorTerms: BalanceEquationTermV2[]
  ratio: RateBoundsV2
}

export type StaleHoldsModeV2 = "stale" | "approaching"
export type StaleHoldsScopeV2 = "per_hold" | "aggregate"

/**
 * How a deadline is written on the account. `datetime` is a key the ledger
 * declares as a datetime; the `epoch_*` encodings are integer keys in the named
 * unit. Whichever it is, the key must be declared AND indexed on the ledger —
 * the service pushes the date comparison down to it.
 */
export type InstantEncodingV2 =
  | "datetime"
  | "epoch_seconds"
  | "epoch_millis"
  | "epoch_micros"

export interface HoldDeadlineV2 {
  expiryKey?: string
  createdKey?: string
  encoding?: InstantEncodingV2
  maxAge?: string
}

export interface StaleHoldsSpecV2 {
  source: LedgerNamedSourceV2
  deadline: HoldDeadlineV2
  mode?: StaleHoldsModeV2
  warnWithin?: string
  scope?: StaleHoldsScopeV2
  /** Metadata keys copied onto each flagged hold's evidence. Labels, not filters: no index needed. */
  identityKeys?: string[]
  /** Per-rule read cap. per_hold defaults to 1000 server-side; never exceeds the engine budget. */
  maxHoldsScanned?: number
}

export type TemplateSpecV2 =
  | BalanceEquationSpecV2
  | ExchangeRateBoundsSpecV2
  | SourceConsensusSpecV2
  | CoverageRatioBoundsSpecV2
  | StaleHoldsSpecV2

interface RuleCommonV2 {
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

export type RuleV2 =
  | (RuleCommonV2 & {
      templateKind: "balance_equation"
      templateSpec: BalanceEquationSpecV2
    })
  | (RuleCommonV2 & {
      templateKind: "exchange_rate_bounds"
      templateSpec: ExchangeRateBoundsSpecV2
    })
  | (RuleCommonV2 & {
      templateKind: "source_consensus"
      templateSpec: SourceConsensusSpecV2
    })
  | (RuleCommonV2 & {
      templateKind: "coverage_ratio_bounds"
      templateSpec: CoverageRatioBoundsSpecV2
    })
  | (RuleCommonV2 & {
      templateKind: "stale_holds"
      templateSpec: StaleHoldsSpecV2
    })

interface RuleRequestCommonV2 {
  name: string
  severity?: Severity
  periodType?: PeriodType
  schedule?: Schedule
  notifications?: string[]
  labels?: Record<string, string>
  enabled?: boolean
}

export type RuleRequestV2 =
  | (RuleRequestCommonV2 & {
      templateKind: "balance_equation"
      templateSpec: BalanceEquationSpecV2
    })
  | (RuleRequestCommonV2 & {
      templateKind: "exchange_rate_bounds"
      templateSpec: ExchangeRateBoundsSpecV2
    })
  | (RuleRequestCommonV2 & {
      templateKind: "source_consensus"
      templateSpec: SourceConsensusSpecV2
    })
  | (RuleRequestCommonV2 & {
      templateKind: "coverage_ratio_bounds"
      templateSpec: CoverageRatioBoundsSpecV2
    })
  | (RuleRequestCommonV2 & {
      templateKind: "stale_holds"
      templateSpec: StaleHoldsSpecV2
    })

export interface RulePatchRequestV2 {
  name?: string
  templateKind?: TemplateKindV2
  templateSpec?: TemplateSpecV2
  enabled?: boolean
  severity?: Severity
  schedule?: Schedule
  notifications?: string[]
  labels?: Record<string, string>
}

export interface EvidenceSourceV2 {
  id: string
  label?: string
  kind: "ledger" | "account_metadata"
  asset: string
  balance: string
  present: boolean
}

export interface BalanceEquationEvidenceSourceV2 extends EvidenceSourceV2 {
  coefficient: number
  contribution: string
}

export interface BalanceEquationEvidenceV2 {
  schemaVersion: 2
  operation: "balance_equation"
  asset: string
  sources: BalanceEquationEvidenceSourceV2[]
  residual: string
  absoluteResidual: string
  tolerance: string
  compiledCEL: string
}

export interface RationalV2 {
  numerator: string
  denominator: string
}

export interface EffectiveRateBoundsV2 {
  min: string
  max: string
}

export interface ExchangeRateBoundsEvidenceV2 {
  schemaVersion: 2
  operation: "exchange_rate_bounds"
  base: EvidenceSourceV2
  quote: EvidenceSourceV2
  observedRate?: RationalV2
  effectiveBounds: EffectiveRateBoundsV2
  undefinedReason?: "base_balance_zero"
  compiledCEL: string
}

export interface SourceConsensusEvidenceV2 {
  schemaVersion: 2
  operation: "source_consensus"
  asset: string
  sources: EvidenceSourceV2[]
  minimumSource: string
  minimumBalance: string
  maximumSource: string
  maximumBalance: string
  spread: string
  tolerance: string
  missingSources: string[]
  compiledCEL: string
}

export interface CoveragePortfolioV2 {
  sources: BalanceEquationEvidenceSourceV2[]
  total: string
}

export interface CoverageRatioBoundsEvidenceV2 {
  schemaVersion: 2
  operation: "coverage_ratio_bounds"
  asset: string
  numerator: CoveragePortfolioV2
  denominator: CoveragePortfolioV2
  observedRatio?: RationalV2
  effectiveBounds: EffectiveRateBoundsV2
  undefinedReason?: "denominator_total_zero"
  compiledCEL: string
}

interface StaleHoldsEvidenceCommonV2 {
  schemaVersion: 2
  operation: "stale_holds"
  mode: StaleHoldsModeV2
  asset: string
  sourceId: string
  evaluatedAt: string
  compiledCEL: string
}

/** One flagged hold: what is stuck, for how much, and how far past its deadline. */
export interface StaleHoldEvidenceV2 extends StaleHoldsEvidenceCommonV2 {
  hold: string
  amount: string
  basis: "expiry" | "created_at"
  deadline: string
  /** mode "stale" only. */
  overdueSeconds?: number
  /** mode "approaching" only. */
  dueInSeconds?: number
  /** The rule's identityKeys that this hold carries — absent when it carries none. */
  identity?: Record<string, string>
}

/**
 * The scan behind an outcome: emitted for an aggregate rule, and for a per_hold
 * rule that found nothing (so a clean run still records what was checked).
 */
export interface StaleHoldsSummaryEvidenceV2 extends StaleHoldsEvidenceCommonV2 {
  scope: StaleHoldsScopeV2
  deadlineOnOrBefore: string
  /** The band's lower bound — mode "approaching" only. */
  deadlineAfter?: string
  holdsMatched: number
  holdsBudget: number
  holdsReleased: number
  holdsFlagged: number
  amountFlagged: string
  oldestDeadline?: string
  /** Bounded per-hold breakdown — aggregate scope only. */
  holds?: StaleHoldEvidenceV2[]
  holdsSampled?: number
}

export type StaleHoldsEvidenceV2 =
  | StaleHoldEvidenceV2
  | StaleHoldsSummaryEvidenceV2

export type EvidenceV2 =
  | BalanceEquationEvidenceV2
  | ExchangeRateBoundsEvidenceV2
  | SourceConsensusEvidenceV2
  | CoverageRatioBoundsEvidenceV2
  | StaleHoldsEvidenceV2

export interface OutcomeV2 {
  fingerprint: string
  passed: boolean
  evidence: EvidenceV2
}

export interface EvaluationV2 {
  id: string
  contractVersion: 2
  ruleID: string
  startedAt: string
  endedAt: string
  result: EvaluationResult
  evidence?: OutcomeV2[] | Record<string, unknown>
  error?: string
  costUnits?: number
  createdAt: string
}

export interface CaptureV2 {
  transactionID: number
  contractVersion: 2
  ruleID: string
  periodID: string
  evaluationID: string
  templateKind: TemplateKindV2
  verdict: Verdict
  trigger: Trigger
  capturedAt: string
  ruleRevision?: string
  pit?: string
  startedAt?: string
  result?: EvaluationResult
  error?: string
  evidence?: OutcomeV2[] | Record<string, unknown>
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
  evidenceSnapshot?: EvidenceV2
  expiresAt?: string
  actor?: Actor
}
interface SnoozeV2 {
  until: string
  by: string
  at: string
  note?: string
}

export interface AlertV2 {
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
  evidence?: EvidenceV2
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

export type RuleResponseV2 = Data<RuleV2>
export type RulesResponseV2 = CursorResponse<RuleV2>
export type EvaluationResponseV2 = Data<EvaluationV2>
export type CapturesResponseV2 = CursorResponse<CaptureV2>
export type RuleActivitiesResponseV2 = CursorResponse<RuleActivity>
export type AlertResponseV2 = Data<AlertV2>
export type AlertsResponseV2 = CursorResponse<AlertV2>
export type AlertEventsResponseV2 = CursorResponse<AlertEventV2>

export type {
  AckAlertRequest,
  AcceptAlertRequest,
  EvaluateRuleRequest,
  ResolveAlertRequest,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
}
