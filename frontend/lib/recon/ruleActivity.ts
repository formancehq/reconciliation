import type {
  AlertActivityPayload,
  AlertLifecycleActivity,
  EvaluationActivityPayload,
  EvaluationCompletedActivity,
  EvaluationResult,
  RuleActivity,
  RuleLifecycleActivity,
  Trigger,
  Verdict,
} from "./types"
import type { Rule } from "./typesV2"

export interface RuleRunGroup {
  type: "run"
  correlationID: string
  activities: RuleActivity[]
}

export interface StandaloneRuleActivity {
  type: "activity"
  activity: RuleActivity
}

export type RuleTimelineItem = RuleRunGroup | StandaloneRuleActivity

export interface EvaluationActivityDetails {
  evaluationID: string
  templateKind?: string
  periodID?: string
  capturedAt: string
  verdict: Verdict
  trigger?: Trigger
  evidence?: unknown
  ruleRevision?: string
  pit?: string
  startedAt?: string
  result: EvaluationResult
  error?: string
}

export interface AlertActivityDetails {
  alertID?: string
  prevStatus?: string
  newStatus?: string
  payload?: Record<string, unknown>
}

export interface ConfigurationFieldDiff {
  path: string
  before?: unknown
  after?: unknown
}

const CONFIGURATION_FIELDS = [
  "name",
  "templateKind",
  "templateSpec",
  "enabled",
  "severity",
  "periodType",
  "schedule",
  "notifications",
  "labels",
] as const

/** Group correlated evaluation and alert facts without changing backend order. */
export function groupRuleActivities(
  activities: RuleActivity[]
): RuleTimelineItem[] {
  const items: RuleTimelineItem[] = []
  const runs = new Map<string, RuleRunGroup>()
  for (const activity of activities) {
    if (activity.correlationID) {
      const existing = runs.get(activity.correlationID)
      if (existing) {
        existing.activities.push(activity)
      } else {
        const group: RuleRunGroup = {
          type: "run",
          correlationID: activity.correlationID,
          activities: [activity],
        }
        runs.set(activity.correlationID, group)
        items.push(group)
      }
    } else {
      items.push({ type: "activity", activity })
    }
  }
  return items
}

/** Append an older cursor page, deduplicating offset-cursor overlap by id. */
export function appendRuleActivities(
  current: RuleActivity[],
  older: RuleActivity[]
): RuleActivity[] {
  const seen = new Set(current.map((activity) => activity.id))
  return [
    ...current,
    ...older.filter((activity) => {
      if (seen.has(activity.id)) return false
      seen.add(activity.id)
      return true
    }),
  ]
}

export function evaluationActivityDetails(
  activity: EvaluationCompletedActivity
): EvaluationActivityDetails {
  const payload = activity.payload ?? {}
  const result = read<EvaluationResult>(payload, "Result", "result")
  const verdict =
    read<Verdict>(payload, "Verdict", "verdict") ??
    (result ? (result.toLowerCase() as Verdict) : "error")
  return {
    evaluationID:
      read<string>(payload, "EvaluationID", "evaluationID") ??
      activity.correlationID ??
      "",
    templateKind: read<string>(payload, "TemplateKind", "templateKind"),
    periodID: read<string>(payload, "PeriodID", "periodID"),
    capturedAt:
      read<string>(payload, "CapturedAt", "capturedAt") ?? activity.occurredAt,
    verdict,
    trigger: read<Trigger>(payload, "Trigger", "trigger"),
    evidence: read<unknown>(payload, "Evidence", "evidence"),
    ruleRevision:
      read<string>(payload, "RuleRevision", "ruleRevision") ??
      activity.ruleRevision,
    pit: read<string>(payload, "PIT", "pit"),
    startedAt: read<string>(payload, "StartedAt", "startedAt"),
    result: result ?? (verdict.toUpperCase() as EvaluationResult),
    error: read<string>(payload, "Error", "error"),
  }
}

export function alertActivityDetails(
  activity: AlertLifecycleActivity
): AlertActivityDetails {
  const payload = activity.payload as AlertActivityPayload | undefined
  return {
    alertID: payload?.alertID,
    prevStatus: payload?.prevStatus,
    newStatus: payload?.newStatus,
    payload: payload?.payload,
  }
}

export function activitySnapshot(
  activity: RuleActivity
): Record<string, unknown> | undefined {
  if (activity.category !== "rule") return undefined
  const snapshot = (activity as RuleLifecycleActivity).payload?.snapshot
  return isRecord(snapshot) ? snapshot : undefined
}

/** Recover a deleted rule from the backend-provided final snapshot. */
export function recoverDeletedRule(
  activities: RuleActivity[],
  contractVersion: 1 | 2
): Rule | undefined {
  const deleted = activities.find(
    (activity) => activity.kind === "rule.deleted"
  )
  if (!deleted) return undefined
  const snapshot = activitySnapshot(deleted)
  return ruleFromActivitySnapshot(snapshot, contractVersion)
}

/** Restore the typed presentation shape without changing the journal payload. */
export function ruleFromActivitySnapshot(
  snapshot: Record<string, unknown> | undefined,
  contractVersion: 1 | 2
): Rule | undefined {
  if (!isRuleShape(snapshot)) return undefined
  return contractVersion === 2
    ? ({ ...snapshot, contractVersion: 2 } as unknown as Rule)
    : (snapshot as unknown as Rule)
}

export function latestEvaluationActivity(
  activities: RuleActivity[]
): EvaluationCompletedActivity | undefined {
  return activities.find(
    (activity): activity is EvaluationCompletedActivity =>
      activity.kind === "evaluation.completed"
  )
}

/**
 * Compare only two snapshots that are actually loaded. Callers decide which
 * snapshots are adjacent; an absent older snapshot deliberately yields no diff.
 */
export function diffConfigurationSnapshots(
  newer: Record<string, unknown> | undefined,
  older: Record<string, unknown> | undefined
): ConfigurationFieldDiff[] {
  if (!newer || !older) return []
  const after = flattenConfiguration(newer)
  const before = flattenConfiguration(older)
  const paths = new Set([...before.keys(), ...after.keys()])
  return [...paths]
    .sort()
    .filter((path) => !deepEqual(before.get(path), after.get(path)))
    .map((path) => ({
      path,
      before: before.get(path),
      after: after.get(path),
    }))
}

export function previousLoadedRuleSnapshot(
  activities: RuleActivity[],
  activityIndex: number
): Record<string, unknown> | undefined {
  for (let index = activityIndex + 1; index < activities.length; index += 1) {
    const snapshot = activitySnapshot(activities[index]!)
    if (snapshot) return snapshot
  }
  return undefined
}

export function legacyTimelineStart(
  activities: RuleActivity[],
  hasMore: boolean
): string | undefined {
  if (hasMore || activities.length === 0) return undefined
  const oldest = activities.at(-1)
  return oldest && oldest.kind !== "rule.created"
    ? oldest.occurredAt
    : undefined
}

export function activityDurationMs(
  startedAt: string | undefined,
  endedAt: string
): number | undefined {
  if (!startedAt) return undefined
  const start = new Date(startedAt).getTime()
  const end = new Date(endedAt).getTime()
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start)
    return undefined
  return end - start
}

function read<T>(
  payload: EvaluationActivityPayload,
  exactKey: keyof EvaluationActivityPayload,
  aliasKey: keyof EvaluationActivityPayload
): T | undefined {
  const value = payload[exactKey] ?? payload[aliasKey]
  return value === undefined || value === null ? undefined : (value as T)
}

function flattenConfiguration(
  snapshot: Record<string, unknown>
): Map<string, unknown> {
  const output = new Map<string, unknown>()
  for (const key of CONFIGURATION_FIELDS) {
    if (key in snapshot) flattenValue(snapshot[key], key, output)
  }
  return output
}

function flattenValue(
  value: unknown,
  path: string,
  output: Map<string, unknown>
) {
  if (Array.isArray(value)) {
    if (value.length === 0) output.set(path, value)
    value.forEach((item, index) => flattenValue(item, `${path}[${index}]`, output))
    return
  }
  if (isRecord(value)) {
    const entries = Object.entries(value)
    if (entries.length === 0) output.set(path, value)
    for (const [key, child] of entries)
      flattenValue(child, `${path}.${key}`, output)
    return
  }
  output.set(path, value)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function isRuleShape(
  value: Record<string, unknown> | undefined
): value is Record<string, unknown> {
  return Boolean(
    value &&
      typeof value.id === "string" &&
      typeof value.name === "string" &&
      typeof value.templateKind === "string" &&
      isRecord(value.templateSpec)
  )
}

function deepEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right)
}
