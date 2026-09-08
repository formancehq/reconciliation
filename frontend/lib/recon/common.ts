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

export type PeriodType = "continuous" | "daily" | "weekly" | "monthly";

export type AlertStatus = "OPEN" | "ACKNOWLEDGED" | "RESOLVED";

export type ResolutionKind = "auto" | "fixed_by_booking" | "accepted_by_business";

export type Verdict = "pass" | "fail" | "error";

export type Trigger = "scheduled" | "manual";

export type EvaluationResult = "PASS" | "FAIL" | "ERROR";

/**
 * The trigger of an alert event. /alerts/{id}/events is backed by a projection
 * of the control ledger's activity stream (ledgerstore.ListAlertEvents): every
 * alert transition is a committed ledger write, and the reader filters that
 * stream to one alert, newest-first, cursor-paginated. Each event carries the
 * `transactionId` of its ledger write (covered by the signed audit chain).
 */
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

/** How an actor identity was obtained, and therefore how far it can be trusted. */
export type ActorSource = "token" | "declared";

/**
 * Who performed a lifecycle transition, with provenance. `source: "token"` means
 * `subject` is the verified subject of an authenticated token (authoritative);
 * `source: "declared"` means only the self-declared `declared` name is known.
 */
export interface Actor {
  source: ActorSource;
  subject?: string;
  declared?: string;
}

export interface Ack {
  by: string;
  at: string;
  note?: string;
  actor?: Actor;
}

/**
 * `Evidence` narrows `evidenceSnapshot` for callers that know the shape. It is a
 * parameter rather than an import because the typed evidence lives in ./types,
 * which imports this module — taking it as a parameter keeps the layering
 * one-way.
 */
export interface Resolution<Evidence = Record<string, unknown>> {
  kind: ResolutionKind;
  by: string;
  at: string;
  note?: string;
  transactionRefs?: string[];
  evidenceSnapshot?: Evidence;
  expiresAt?: string;
  actor?: Actor;
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




// ---------------------------------------------------------------------------
// Evaluation (POST /rules/{id}/evaluate)
// ---------------------------------------------------------------------------

export interface EvaluateRuleRequest {
  /** PIT for the evaluation. Defaults to now. */
  at?: string;
}


// ---------------------------------------------------------------------------
// Captures (GET /rules/{id}/captures) — the LIVE evaluation history
// ---------------------------------------------------------------------------


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


/** One row of the alert's append-only timeline (see AlertEventType). */
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
  /**
   * Control-ledger transaction id of the write behind this event. That write is
   * covered by the signed audit chain; it is the tx id, not the audit sequence
   * that indexes GET /audit/entries.
   */
  transactionId?: string;
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


/**
 * A public signing key the control-ledger writes are signed with. `publicKey`
 * is standard base64 — what an auditor feeds to ed25519.Verify.
 */
export interface SigningKey {
  keyId: string;
  publicKey: string;
  parentKeyId?: string;
}
export type SigningKeysResponse = Data<{ keys: SigningKey[] }>;

/**
 * One entry of the ledger's native audit trail, scoped to the control ledger.
 * `payload` (the committed batch bytes) and `signature` are standard base64; an
 * auditor verifies with ed25519.Verify(publicKey, payload, signature). `sequence`
 * is the ledger's dense, bucket-wide audit sequence.
 */
export interface AuditEntry {
  sequence: number;
  timestamp?: string;
  keyId?: string;
  payload?: string;
  signature?: string;
  signed: boolean;
  outcome: "success" | "failure";
  orderCount: number;
  ledgers?: string[];
  /** Populated only for outcome === "failure": why the write was rejected. */
  failureReason?: string;
  failureMessage?: string;
  /**
   * Decoded per-order business intent. Populated only on the single-entry read
   * (getAuditEntry); the list omits it. An Apply batch is one action, reported
   * without decoding its numscript.
   */
  actions?: AuditAction[];
}

/** The human-readable intent of one order in an audit proposal. */
export interface AuditAction {
  kind: string;
  ledger?: string;
  detail?: string;
}
export type AuditEntriesResponse = Data<{ entries: AuditEntry[] }>;
export type AuditEntryResponse = Data<AuditEntry>;
