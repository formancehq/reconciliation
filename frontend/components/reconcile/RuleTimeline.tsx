"use client"

import { Fragment, useState } from "react"
import {
  AlertCircle,
  Bell,
  BellOff,
  CheckCircle2,
  ChevronDown,
  CircleAlert,
  Clock3,
  Copy,
  FileClock,
  History,
  Loader2,
  Pencil,
  RefreshCw,
  Settings2,
  ShieldCheck,
  Trash2,
  TriangleAlert,
  XCircle,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  DescriptionDetails,
  DescriptionList,
  DescriptionTerm,
} from "@workspace/ui/components/description-list"
import { cn } from "@workspace/ui/lib/utils"
import {
  activityDurationMs,
  activitySnapshot,
  alertActivityDetails,
  diffConfigurationSnapshots,
  evaluationActivityDetails,
  evidenceGroups,
  evidenceLabel,
  formatDateTime,
  formatRelative,
  groupRuleActivities,
  legacyTimelineStart,
  previousLoadedRuleSnapshot,
  templateLabel,
  type AlertLifecycleActivity,
  type EvaluationCompletedActivity,
  type RuleActivity,
} from "@/lib/recon"
import { ResultBadge } from "./ui"
import { EvidenceView, isEvidence } from "./EvidenceView"
import { RevisionSnapshot } from "./RevisionSnapshot"

export function RuleTimeline({
  activities,
  currentRevision,
  hasMore,
  loading,
  loadingEarlier,
  error,
  onRetry,
  onLoadEarlier,
  onOpenAlert,
  heading = "Combined history",
  dense = false,
}: {
  activities: RuleActivity[]
  currentRevision?: string
  hasMore: boolean
  loading: boolean
  loadingEarlier: boolean
  error?: unknown
  onRetry?: () => void
  onLoadEarlier: () => void
  onOpenAlert?: (alertID: string, contractVersion: 1 | 2) => void
  heading?: string
  /** Collapse each entry's detail (facts, evidence, snapshots) behind a compact,
   *  expandable summary — for space-constrained surfaces like the alert detail. */
  dense?: boolean
}) {
  const items = groupRuleActivities(activities)
  const legacyStart = legacyTimelineStart(activities, hasMore)

  return (
    <section aria-labelledby="rule-activity-heading" className="min-w-0">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <History className="h-4 w-4 text-muted-foreground" />
        <h3 id="rule-activity-heading" className="text-sm font-semibold">
          {heading}
        </h3>
        <span className="text-xs text-muted-foreground">
          {activities.length} activit{activities.length === 1 ? "y" : "ies"}
        </span>
      </div>

      {loading && activities.length === 0 ? (
        <div className="flex min-h-32 items-center justify-center gap-2 rounded-md border border-dashed text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading rule activity…
        </div>
      ) : error && activities.length === 0 ? (
        <div role="alert" className="rounded-md border p-4 text-sm">
          <div className="flex items-center gap-2 font-medium">
            <AlertCircle className="h-4 w-4 text-destructive-foreground" />
            Timeline activity could not be loaded
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {error instanceof Error ? error.message : String(error)}
          </p>
          {onRetry && (
            <Button className="mt-3" size="sm" variant="outline" onClick={onRetry}>
              <RefreshCw className="mr-1.5 h-3.5 w-3.5" /> Retry
            </Button>
          )}
        </div>
      ) : activities.length === 0 ? (
        <div className="rounded-md border border-dashed px-4 py-8 text-center">
          <FileClock className="mx-auto h-6 w-6 text-muted-foreground" />
          <p className="mt-2 text-sm font-medium">No activity recorded yet</p>
          <p className="mt-1 text-xs text-muted-foreground">
            New rule, evaluation, and alert lifecycle facts will appear here.
          </p>
        </div>
      ) : (
        <>
          <ol
            data-testid="rule-activity-timeline"
            className="relative touch-pan-y space-y-3 border-l pl-5"
          >
            {items.map((item) =>
              item.type === "run" ? (
                <RunGroupCard
                  key={`run:${item.correlationID}`}
                  correlationID={item.correlationID}
                  activities={item.activities}
                  currentRevision={currentRevision}
                  onOpenAlert={onOpenAlert}
                  dense={dense}
                />
              ) : (
                <StandaloneActivityCard
                  key={item.activity.id}
                  activity={item.activity}
                  allActivities={activities}
                  onOpenAlert={onOpenAlert}
                />
              )
            )}
          </ol>

          {legacyStart && (
            <div className="mt-4 rounded-md border bg-muted/25 px-3 py-2 text-xs text-muted-foreground">
              Timeline activity is available from {formatDateTime(legacyStart)}.
              Earlier lifecycle history was not recorded in this journal.
            </div>
          )}

          {hasMore && (
            <div className="mt-4 flex justify-center">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={loadingEarlier}
                onClick={onLoadEarlier}
              >
                {loadingEarlier ? (
                  <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                ) : (
                  <ChevronDown className="mr-1.5 h-3.5 w-3.5" />
                )}
                Load earlier activity
              </Button>
            </div>
          )}
        </>
      )}
    </section>
  )
}

function RunGroupCard({
  correlationID,
  activities,
  currentRevision,
  onOpenAlert,
  dense = false,
}: {
  correlationID: string
  activities: RuleActivity[]
  currentRevision?: string
  onOpenAlert?: (alertID: string, contractVersion: 1 | 2) => void
  dense?: boolean
}) {
  const [open, setOpen] = useState(false)
  const evaluation = activities.find(
    (activity): activity is EvaluationCompletedActivity =>
      activity.kind === "evaluation.completed"
  )
  const details = evaluation ? evaluationActivityDetails(evaluation) : undefined
  const duration = details
    ? activityDurationMs(details.startedAt, details.capturedAt)
    : undefined
  const revisionMismatch = Boolean(
    currentRevision &&
      details?.ruleRevision &&
      currentRevision !== details.ruleRevision
  )

  const tone =
    details?.result === "PASS"
      ? "success"
      : details?.result === "ERROR"
        ? "warning"
        : "danger"
  const icon =
    details?.result === "PASS" ? (
      <CheckCircle2 />
    ) : details?.result === "ERROR" ? (
      <TriangleAlert />
    ) : (
      <XCircle />
    )

  const facts = (
    <div className="grid gap-2 border-b px-3 py-2.5 text-xs sm:grid-cols-2 lg:grid-cols-4">
      <RunFact label="Evaluation" value={shortID(correlationID)} title={correlationID} />
      <RunFact
        label="Template"
        value={details?.templateKind ? templateLabel(details.templateKind) : "Pending older activity"}
      />
      <RunFact label="Duration" value={formatDuration(duration)} />
      <div className="min-w-0">
        <div className="text-[10px] tracking-wide text-muted-foreground uppercase">Rule revision</div>
        <RevisionValue revision={details?.ruleRevision} />
      </div>
    </div>
  )
  const mismatchBanner = revisionMismatch ? (
    <div className="border-b border-amber-foreground/30 bg-warning px-3 py-2 text-xs text-warning-foreground">
      This evaluation used a different rule revision from the current configuration.
    </div>
  ) : null
  const activityList = (
    <ol aria-label={`Activity for evaluation ${correlationID}`} className="divide-y">
      {activities.map((activity) => (
        <li key={activity.id} className="min-w-0 px-3 py-3">
          {activity.kind === "evaluation.completed" ? (
            <EvaluationActivityBody activity={activity} />
          ) : (
            <AlertActivityBody
              activity={activity as AlertLifecycleActivity}
              onOpenAlert={onOpenAlert}
            />
          )}
        </li>
      ))}
    </ol>
  )

  // Dense: lead with the alert transition(s) this run produced (the narrative)
  // and tuck the facts + evidence behind a click. Falls back to the evaluation
  // result when the run carries no alert transition.
  if (dense) {
    const transitions = activities.filter(
      (activity) => activity.category === "alert"
    )
    const summary =
      transitions.length > 0
        ? transitions.map((activity) => activityLabel(activity.kind)).join(" · ")
        : details
          ? `Evaluation ${details.result.toLowerCase()}`
          : "Evaluation activity"
    return (
      <li className="relative min-w-0">
        <TimelineDot tone={tone} icon={icon} />
        <Card className="min-w-0 overflow-hidden">
          <button
            type="button"
            onClick={() => setOpen((value) => !value)}
            aria-expanded={open}
            className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-muted/25"
          >
            <ChevronDown
              className={cn(
                "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                !open && "-rotate-90"
              )}
            />
            {details && <ResultBadge result={details.result} />}
            <span className="min-w-0 flex-1 truncate text-sm font-medium">
              {summary}
            </span>
            {details?.periodID && (
              <span className="hidden text-xs text-muted-foreground sm:inline">
                {details.periodID}
              </span>
            )}
            <ActivityTime
              className="shrink-0"
              value={details?.capturedAt ?? activities[0]!.occurredAt}
            />
          </button>
          {open && (
            <div className="border-t">
              {facts}
              {mismatchBanner}
              {activityList}
            </div>
          )}
        </Card>
      </li>
    )
  }

  return (
    <li className="relative min-w-0">
      <TimelineDot tone={tone} icon={icon} />
      <Card className="min-w-0 overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b bg-muted/25 px-3 py-2.5">
          {details ? <ResultBadge result={details.result} /> : <span className="text-xs font-medium">Evaluation activity</span>}
          {details?.trigger && (
            <span className="text-xs text-muted-foreground">
              {details.trigger === "manual" ? "Manual" : "Scheduled"}
            </span>
          )}
          {details?.periodID && (
            <span className="text-xs text-muted-foreground">· {details.periodID}</span>
          )}
          {details && (
            <ActivityTime className="ml-auto" value={details.capturedAt} />
          )}
        </div>
        {facts}
        {mismatchBanner}
        {activityList}
      </Card>
    </li>
  )
}

function EvaluationActivityBody({
  activity,
}: {
  activity: EvaluationCompletedActivity
}) {
  const details = evaluationActivityDetails(activity)
  const groups = evidenceGroups(details.evidence)
  return (
    <div className="min-w-0 space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">
          {details.result === "PASS"
            ? "Evaluation passed"
            : details.result === "FAIL"
              ? "Evaluation failed"
              : "Evaluation error"}
        </span>
        <span className="ml-auto font-mono text-[10px] text-muted-foreground">
          sequence {activity.sequence}
        </span>
      </div>
      {details.startedAt && (
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <span>Started <ActivityTime value={details.startedAt} relative={false} /></span>
          <span>Ended <ActivityTime value={details.capturedAt} relative={false} /></span>
          {details.pit && <span>PIT <ActivityTime value={details.pit} relative={false} /></span>}
        </div>
      )}
      {details.result === "ERROR" && (
        <div role="alert" className="flex items-start gap-2 rounded-md border border-amber-foreground/30 bg-warning px-3 py-2 text-sm text-warning-foreground">
          <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{details.error || "The evaluation did not produce a verdict."}</span>
        </div>
      )}
      {groups.map((group, index) => (
        <EvidenceGroup key={group.fingerprint ?? index} group={group} />
      ))}
    </div>
  )
}

function EvidenceGroup({
  group,
}: {
  group: ReturnType<typeof evidenceGroups>[number]
}) {
  if (!group.fingerprint && group.rows.length === 0) return null
  return (
    <div className="min-w-0 rounded-md bg-muted/30 p-2">
      {group.fingerprint && (
        <div className="mb-1 font-mono text-[11px] text-muted-foreground">
          {group.fingerprint}
        </div>
      )}
      {isEvidence(group.evidence) ? (
        <EvidenceView evidence={group.evidence} passed={group.passed} />
      ) : (
        group.rows.length > 0 && (
          <DescriptionList>
            {group.rows.map(({ key, value }) => (
              <Fragment key={key}>
                <DescriptionTerm className="text-xs text-muted-foreground">
                  {evidenceLabel(key)}
                </DescriptionTerm>
                <DescriptionDetails className="text-xs break-all">
                  {value}
                </DescriptionDetails>
              </Fragment>
            ))}
          </DescriptionList>
        )
      )}
    </div>
  )
}

function StandaloneActivityCard({
  activity,
  allActivities,
  onOpenAlert,
}: {
  activity: RuleActivity
  allActivities: RuleActivity[]
  onOpenAlert?: (alertID: string, contractVersion: 1 | 2) => void
}) {
  const ruleEvent = activity.category === "rule"
  const snapshot = activitySnapshot(activity)
  const index = allActivities.findIndex((candidate) => candidate.id === activity.id)
  const olderSnapshot = previousLoadedRuleSnapshot(allActivities, index)
  const diff = ruleEvent
    ? diffConfigurationSnapshots(snapshot, olderSnapshot)
    : []

  return (
    <li className="relative min-w-0">
      <TimelineDot
        tone={activity.kind === "rule.deleted" ? "danger" : "neutral"}
        icon={activityIcon(activity.kind)}
      />
      <Card className="min-w-0 p-3">
        <div className="flex flex-wrap items-start gap-2">
          <div>
            <div className="text-sm font-medium">{activityLabel(activity.kind)}</div>
            <div className="mt-0.5 font-mono text-[10px] text-muted-foreground">
              sequence {activity.sequence}
            </div>
          </div>
          <ActivityTime className="ml-auto" value={activity.occurredAt} />
        </div>

        {activity.category === "alert" && (
          <AlertActivityBody
            activity={activity as AlertLifecycleActivity}
            onOpenAlert={onOpenAlert}
          />
        )}

        {ruleEvent && snapshot && (
          <div className="mt-3 space-y-2">
            <RevisionValue revision={activity.ruleRevision} />
            <details className="rounded-md border bg-muted/20 text-xs">
              <summary className="cursor-pointer px-3 py-2 font-medium select-none">
                Revision snapshot
              </summary>
              <RevisionSnapshot
                snapshot={snapshot}
                contractVersion={activity.contractVersion}
              />
            </details>
            {diff.length > 0 && (
              <details className="rounded-md border text-xs">
                <summary className="cursor-pointer px-3 py-2 font-medium select-none">
                  Changed fields · {diff.length}
                </summary>
                <dl className="divide-y border-t">
                  {diff.map((change) => (
                    <div key={change.path} className="min-w-0 px-3 py-3">
                      <dt className="min-w-0 break-all font-mono text-[11px] text-muted-foreground">
                        {change.path}
                      </dt>
                      <dd className="mt-2 grid min-w-0 gap-2 sm:grid-cols-2">
                        <DiffValue label="Previous" value={change.before} />
                        <DiffValue label="Current" value={change.after} current />
                      </dd>
                    </div>
                  ))}
                </dl>
              </details>
            )}
          </div>
        )}
      </Card>
    </li>
  )
}

function DiffValue({
  label,
  value,
  current = false,
}: {
  label: string
  value: unknown
  current?: boolean
}) {
  return (
    <div
      className={cn(
        "min-w-0 rounded-md border px-2.5 py-2",
        current ? "bg-muted/40" : "bg-background"
      )}
    >
      <div className="text-[10px] tracking-wide text-muted-foreground uppercase">
        {label}
      </div>
      <code className="mt-1 block min-w-0 whitespace-pre-wrap break-all text-xs">
        {formatValue(value)}
      </code>
    </div>
  )
}

function AlertActivityBody({
  activity,
  onOpenAlert,
}: {
  activity: AlertLifecycleActivity
  onOpenAlert?: (alertID: string, contractVersion: 1 | 2) => void
}) {
  const details = alertActivityDetails(activity)
  const description = describeAlertPayload(details.payload)
  return (
    <div className="mt-2 min-w-0 text-xs">
      <div className="flex flex-wrap items-center gap-2 text-muted-foreground">
        {details.prevStatus && details.newStatus && (
          <span>{details.prevStatus} → {details.newStatus}</span>
        )}
        <span className="font-mono text-[10px]">sequence {activity.sequence}</span>
        {details.alertID && onOpenAlert && (
          <Button
            className="ml-auto h-7"
            size="sm"
            variant="outline"
            onClick={() => onOpenAlert(details.alertID!, activity.contractVersion)}
          >
            Open alert
          </Button>
        )}
      </div>
      {description && <p className="mt-1 break-words">{description}</p>}
    </div>
  )
}

export function RevisionValue({ revision }: { revision?: string }) {
  if (!revision) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <code className="truncate text-xs" title={revision}>{shortRevision(revision)}</code>
      <button
        type="button"
        aria-label={`Copy full revision ${revision}`}
        title="Copy full revision"
        className="shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        onClick={() => void navigator.clipboard?.writeText(revision)}
      >
        <Copy className="h-3 w-3" />
      </button>
    </span>
  )
}

function ActivityTime({
  value,
  relative = true,
  className,
}: {
  value: string
  relative?: boolean
  className?: string
}) {
  return (
    <time
      className={cn("text-xs text-muted-foreground", className)}
      dateTime={value}
      title={formatDateTime(value)}
    >
      {relative ? formatRelative(value) : formatDateTime(value)}
    </time>
  )
}

function TimelineDot({
  icon,
  tone,
}: {
  icon: React.ReactNode
  tone: "success" | "danger" | "warning" | "neutral"
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "absolute -left-[27px] flex h-4 w-4 items-center justify-center rounded-full bg-background ring-4 ring-background [&_svg]:h-4 [&_svg]:w-4",
        tone === "success" && "text-green-foreground",
        tone === "danger" && "text-destructive-foreground",
        tone === "warning" && "text-amber-foreground",
        tone === "neutral" && "text-muted-foreground"
      )}
    >
      {icon}
    </span>
  )
}

function RunFact({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] tracking-wide text-muted-foreground uppercase">{label}</div>
      <div className="truncate font-mono text-xs" title={title ?? value}>{value}</div>
    </div>
  )
}

function activityLabel(kind: RuleActivity["kind"]): string {
  return {
    "rule.created": "Rule created",
    "rule.updated": "Configuration updated",
    "rule.deleted": "Rule deleted",
    "evaluation.completed": "Evaluation completed",
    "alert.opened": "Alert opened",
    "alert.occurred": "Discrepancy observed again",
    "alert.reopened": "Alert reopened",
    "alert.acknowledged": "Acknowledged",
    "alert.resolved": "Resolved after booking",
    "alert.accepted": "Accepted by business",
    "alert.auto_resolved": "Resolved automatically",
    "alert.snoozed": "Snoozed",
    "alert.unsnoozed": "Unsnoozed",
  }[kind]
}

function activityIcon(kind: RuleActivity["kind"]): React.ReactNode {
  if (kind === "rule.created") return <Settings2 />
  if (kind === "rule.updated") return <Pencil />
  if (kind === "rule.deleted") return <Trash2 />
  if (kind === "alert.auto_resolved" || kind === "alert.resolved") return <ShieldCheck />
  if (kind === "alert.snoozed") return <BellOff />
  if (kind === "alert.unsnoozed") return <Bell />
  if (kind === "alert.acknowledged" || kind === "alert.accepted") return <CheckCircle2 />
  if (kind.startsWith("alert.")) return <CircleAlert />
  return <Clock3 />
}

function describeAlertPayload(payload: Record<string, unknown> | undefined): string | undefined {
  if (!payload) return undefined
  const nested = [payload.ack, payload.resolution, payload.snooze].find(isRecord)
  const actor = typeof nested?.by === "string" ? nested.by : typeof payload.by === "string" ? payload.by : undefined
  const note = typeof nested?.note === "string" ? nested.note : undefined
  const count = typeof payload.occurrenceCount === "number" ? payload.occurrenceCount : undefined
  const refs = Array.isArray(nested?.transactionRefs)
    ? nested.transactionRefs.filter((value): value is string => typeof value === "string")
    : []
  return [
    actor ? `by ${actor}` : undefined,
    count !== undefined ? `occurrence ${count}` : undefined,
    refs.length > 0 ? `booking refs ${refs.join(", ")}` : undefined,
    note,
  ].filter(Boolean).join(" · ") || undefined
}

function shortRevision(revision: string): string {
  const [prefix, value] = revision.includes(":")
    ? revision.split(":", 2)
    : [undefined, revision]
  const short = value.slice(0, 12)
  return prefix ? `${prefix}:${short}` : short
}

function shortID(value: string): string {
  return value.length > 12 ? value.slice(0, 8) : value
}

function formatDuration(value: number | undefined): string {
  if (value === undefined) return "—"
  if (value < 1_000) return `${value} ms`
  return `${(value / 1_000).toFixed(value < 10_000 ? 2 : 1)} s`
}

function formatValue(value: unknown): string {
  if (value === undefined) return "not set"
  if (typeof value === "string") return value
  return JSON.stringify(value)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
