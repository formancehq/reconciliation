// Reconciliation ("Ledger Clarity") REST API — TypeScript types.
//
// COPIED VERBATIM from `recon-api/types.ts` (the contract at repo root) so the
// app owns a build-graph copy. Source of truth: `recon-api/openapi.yaml`
// (reconciliation@9abcbfb, branch feat/reconciliation-ledger-v3). If the
// contract changes, re-copy from recon-api/types.ts.
//
// Conventions:
//   - single-object responses are wrapped:   { data: T }
//   - list responses are cursor-paginated:   { cursor: Cursor<T> }
//   - all timestamps are RFC3339 strings (ISO 8601).

// ---------------------------------------------------------------------------
// Envelopes
// ---------------------------------------------------------------------------

export interface Data<T> {
  data: T;
}

export interface Cursor<T> {
  pageSize: number;
  hasMore: boolean;
  previous?: string;
  next?: string;
  // Recon returns `null` (not []) for an empty page — normalize when consuming.
  data: T[] | null;
}

export interface CursorResponse<T> {
  cursor: Cursor<T>;
}

/** Shape of any non-2xx body. */
export interface ErrorResponse {
  errorCode: string; // e.g. "VALIDATION", "NOT_FOUND", "INTERNAL"
  errorMessage: string;
  details?: string;
}

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

export type Severity = "info" | "low" | "medium" | "high" | "critical";

/**
 * The 3 registered V1 GA templates. See TEMPLATE-SPECS.md for each `templateSpec`
 * shape. (`ledger_vs_pool_drift` was removed — sending it returns 400.)
 */
export type TemplateKind =
  | "ledger_invariant"
  | "source_parity"
  | "account_threshold";

export type PeriodType = "continuous" | "daily" | "weekly" | "monthly";

export type AlertStatus = "OPEN" | "ACKNOWLEDGED" | "RESOLVED";

export type ResolutionKind = "auto" | "fixed_by_booking" | "accepted_by_business";

export type Verdict = "pass" | "fail" | "error";

export type Trigger = "scheduled" | "manual";

export type EvaluationResult = "PASS" | "FAIL" | "ERROR";

/** Only relevant once /alerts/{id}/events is backed — it is a stub today (empty). */
export type AlertEventType =
  | "fail"
  | "pass"
  | "ack"
  | "resolve"
  | "accept"
  | "snooze"
  | "unsnooze";

// ---------------------------------------------------------------------------
// Shared sub-objects
// ---------------------------------------------------------------------------

export interface Schedule {
  kind: "on_demand" | "cron";
  expr?: string; // e.g. "*/15 * * * *"
  tz?: string; // e.g. "UTC"
}

export interface Ack {
  by: string;
  at: string;
  note?: string;
}

export interface Resolution {
  kind: ResolutionKind;
  by: string;
  at: string;
  note?: string;
  transactionRefs?: string[];
  evidenceSnapshot?: Record<string, unknown>;
  expiresAt?: string;
}

export interface Snooze {
  until: string;
  by: string;
  at: string;
  note?: string;
}

// ---------------------------------------------------------------------------
// Rules
// ---------------------------------------------------------------------------

export interface Rule {
  id: string;
  revision?: string;
  name: string;
  templateKind: TemplateKind;
  /** Template-specific config (shape depends on templateKind). */
  templateSpec: Record<string, unknown>;
  compiledCEL?: string;
  enabled: boolean;
  severity: Severity;
  periodType: PeriodType;
  schedule?: Schedule;
  notifications?: string[];
  labels?: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

/** POST /rules body. */
export interface RuleRequest {
  name: string;
  templateKind: TemplateKind;
  templateSpec: Record<string, unknown>;
  severity?: Severity;
  periodType?: PeriodType; // defaults to "continuous"
  schedule?: Schedule;
  notifications?: string[];
  labels?: Record<string, string>;
  enabled?: boolean;
}

/** PATCH /rules/{id} body — only supplied fields are applied. */
export interface RulePatchRequest {
  name?: string;
  templateKind?: TemplateKind;
  templateSpec?: Record<string, unknown>;
  enabled?: boolean;
  severity?: Severity;
  schedule?: Schedule;
  notifications?: string[];
  labels?: Record<string, string>;
}

// ---------------------------------------------------------------------------
// Evaluation (POST /rules/{id}/evaluate)
// ---------------------------------------------------------------------------

export interface EvaluateRuleRequest {
  /** PIT for the evaluation. Defaults to now. */
  at?: string;
}

/** Immediate run response; durable evidence is recorded in rule activity. */
export interface Evaluation {
  id: string;
  ruleID: string;
  startedAt: string;
  endedAt: string;
  pitPerSource?: Record<string, string>;
  result: EvaluationResult;
  /** Failing fingerprints only (empty on an all-PASS run). */
  evidence?: Array<Record<string, unknown>> | Record<string, unknown>;
  error?: string;
  costUnits?: number;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// Captures (GET /rules/{id}/captures) — the LIVE evaluation history
// ---------------------------------------------------------------------------

export interface Capture {
  /** Ledger-local id of the underlying capture transaction. */
  transactionID: number;
  ruleID: string;
  periodID: string;
  evaluationID: string;
  templateKind?: string;
  verdict: Verdict;
  trigger: Trigger;
  capturedAt: string;
  ruleRevision?: string;
  pit?: string;
  startedAt?: string;
  result?: EvaluationResult;
  error?: string;
  /**
   * Fingerprint-scoped retained outcomes. Every failing outcome is present;
   * passing outcomes are present only when they automatically resolve an alert.
   * Use the outcome's `passed` value rather than the capture-wide verdict.
   */
  evidence?: unknown;
}

// ---------------------------------------------------------------------------
// Rule activity timeline
// ---------------------------------------------------------------------------

export type RuleActivityKind =
  | "rule.created"
  | "rule.updated"
  | "rule.deleted"
  | "evaluation.completed"
  | "alert.opened"
  | "alert.occurred"
  | "alert.reopened"
  | "alert.acknowledged"
  | "alert.resolved"
  | "alert.accepted"
  | "alert.auto_resolved"
  | "alert.snoozed"
  | "alert.unsnoozed";

export type RuleActivityCategory = "rule" | "evaluation" | "alert";

export interface RuleSnapshotActivityPayload {
  snapshot?: Record<string, unknown>;
  previousRevision?: string;
  [key: string]: unknown;
}

/**
 * The journal currently serializes the backend CaptureInput directly, whose
 * exact wire keys are PascalCase. Optional camelCase aliases make the reader
 * forward-compatible if that backend struct later gains JSON tags.
 */
export interface EvaluationActivityPayload extends Record<string, unknown> {
  TemplateKind?: string;
  PeriodID?: string;
  EvaluationID?: string;
  CapturedAt?: string;
  Verdict?: Verdict;
  Trigger?: Trigger;
  Evidence?: unknown;
  RuleRevision?: string;
  PIT?: string;
  StartedAt?: string;
  Result?: EvaluationResult;
  Error?: string;
  templateKind?: string;
  periodID?: string;
  evaluationID?: string;
  capturedAt?: string;
  verdict?: Verdict;
  trigger?: Trigger;
  evidence?: unknown;
  ruleRevision?: string;
  pit?: string;
  startedAt?: string;
  result?: EvaluationResult;
  error?: string;
}

export interface AlertActivityPayload extends Record<string, unknown> {
  type?: string;
  subject?: string;
  alertID?: string;
  prevStatus?: AlertStatus;
  newStatus?: AlertStatus;
  occurredAt?: string;
  correlationID?: string;
  payload?: Record<string, unknown>;
}

interface RuleActivityBase<
  K extends RuleActivityKind,
  C extends RuleActivityCategory,
  P extends Record<string, unknown>,
> {
  /** Ledger-local activity id; it must remain a string. */
  id: string;
  /** Ledger sequence; it must remain a string. */
  sequence: string;
  kind: K;
  category: C;
  ruleID: string;
  contractVersion: 1 | 2;
  ruleRevision?: string;
  correlationID?: string;
  occurredAt: string;
  recordedAt: string;
  payload?: P;
}

export type RuleLifecycleActivity = RuleActivityBase<
  "rule.created" | "rule.updated" | "rule.deleted",
  "rule",
  RuleSnapshotActivityPayload
>;

export type EvaluationCompletedActivity = RuleActivityBase<
  "evaluation.completed",
  "evaluation",
  EvaluationActivityPayload
>;

export type AlertLifecycleActivity = RuleActivityBase<
  Exclude<RuleActivityKind, "rule.created" | "rule.updated" | "rule.deleted" | "evaluation.completed">,
  "alert",
  AlertActivityPayload
>;

export type RuleActivity =
  | RuleLifecycleActivity
  | EvaluationCompletedActivity
  | AlertLifecycleActivity;

// ---------------------------------------------------------------------------
// Alerts
// ---------------------------------------------------------------------------

export interface Alert {
  id: string;
  ruleID: string;
  fingerprint: string; // e.g. "asset:USD/2"
  periodID: string; // "continuous" for live-monitoring rules
  status: AlertStatus;
  severity: Severity;
  firstSeenAt: string;
  lastSeenAt: string;
  occurrenceCount: number;
  lastEvaluationID: string;
  evidence?: Record<string, unknown>;
  ack?: Ack;
  resolution?: Resolution;
  snooze?: Snooze;
  labels?: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

/** One row of the alert timeline. STUB: /alerts/{id}/events returns [] today. */
export interface AlertEvent {
  id: string;
  alertID: string;
  evaluationID?: string | null;
  type: AlertEventType;
  prevStatus?: AlertStatus | null;
  newStatus: AlertStatus;
  payload?: Record<string, unknown>;
  at: string;
  isReopen: boolean;
  notify: boolean;
}

// --- Alert action request bodies -------------------------------------------

export interface AckAlertRequest {
  by: string;
  note?: string;
}

/** transactionRefs non-empty ⇒ resolution kind recorded as fixed_by_booking. */
export interface ResolveAlertRequest {
  by: string;
  note?: string;
  transactionRefs?: string[];
}

/** Business acceptance — note is required. */
export interface AcceptAlertRequest {
  by: string;
  note: string;
  expiresAt?: string;
}

/** until must be in the future. */
export interface SnoozeAlertRequest {
  by: string;
  until: string;
  note?: string;
}

export interface UnsnoozeAlertRequest {
  by: string;
}

// ---------------------------------------------------------------------------
// Endpoint result aliases (what each call returns)
// ---------------------------------------------------------------------------

export type RuleResponse = Data<Rule>;
export type RulesResponse = CursorResponse<Rule>;
export type EvaluationResponse = Data<Evaluation>;
export type CapturesResponse = CursorResponse<Capture>;
export type RuleActivitiesResponse = CursorResponse<RuleActivity>;
export type AlertResponse = Data<Alert>;
export type AlertsResponse = CursorResponse<Alert>;
export type AlertEventsResponse = CursorResponse<AlertEvent>; // empty today
