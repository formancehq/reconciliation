"use client"

import { useState } from "react"
import {
  AlertCircle,
  Bell,
  BellOff,
  CheckCircle2,
  ChevronDown,
  CircleAlert,
  FileClock,
  History,
  Loader2,
  RefreshCw,
  ShieldCheck,
  XCircle,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { cn } from "@workspace/ui/lib/utils"
import { formatDateTime, formatRelative, type AlertEvent } from "@/lib/recon"

/**
 * The alert's own append-only event log, straight from GET /alerts/{id}/events
 * (a projection of the control-ledger activity stream). One row per transition,
 * newest-first, cursor-paginated — so a long-lived alert loads a page at a time
 * instead of scanning the whole rule history client-side.
 */
export function AlertEventsTimeline({
  events,
  hasMore,
  loading,
  loadingMore,
  error,
  onRetry,
  onLoadMore,
  heading = "Timeline",
  hasEvidence,
  renderEvidence,
}: {
  events: AlertEvent[]
  hasMore: boolean
  loading: boolean
  loadingMore: boolean
  error?: unknown
  onRetry?: () => void
  onLoadMore: () => void
  heading?: string
  /** True when the event's evaluation has retained evidence to expand. */
  hasEvidence?: (evaluationID?: string) => boolean
  /** The evaluation's evidence for this alert, rendered lazily on expand. */
  renderEvidence?: (evaluationID?: string) => React.ReactNode
}) {
  return (
    <section aria-labelledby="alert-events-heading" className="min-w-0">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <History className="h-4 w-4 text-muted-foreground" />
        <h3 id="alert-events-heading" className="text-sm font-semibold">
          {heading}
        </h3>
        <span className="text-xs text-muted-foreground">
          {events.length} event{events.length === 1 ? "" : "s"}
          {hasMore ? "+" : ""}
        </span>
      </div>

      {loading && events.length === 0 ? (
        <div className="flex min-h-32 items-center justify-center gap-2 rounded-md border border-dashed text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading timeline…
        </div>
      ) : error && events.length === 0 ? (
        <div role="alert" className="rounded-md border p-4 text-sm">
          <div className="flex items-center gap-2 font-medium">
            <AlertCircle className="h-4 w-4 text-destructive-foreground" />
            Timeline could not be loaded
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
      ) : events.length === 0 ? (
        <div className="rounded-md border border-dashed px-4 py-8 text-center">
          <FileClock className="mx-auto h-6 w-6 text-muted-foreground" />
          <p className="mt-2 text-sm font-medium">No events recorded yet</p>
          <p className="mt-1 text-xs text-muted-foreground">
            Every evaluation and manual transition on this alert will appear here.
          </p>
        </div>
      ) : (
        <>
          <ol
            data-testid="alert-events-timeline"
            className="relative touch-pan-y space-y-3 border-l pl-5"
          >
            {events.map((event) => (
              <AlertEventRow
                key={event.id}
                event={event}
                hasEvidence={hasEvidence}
                renderEvidence={renderEvidence}
              />
            ))}
          </ol>

          {hasMore && (
            <div className="mt-4 flex justify-center">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={loadingMore}
                onClick={onLoadMore}
              >
                {loadingMore ? (
                  <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                ) : (
                  <ChevronDown className="mr-1.5 h-3.5 w-3.5" />
                )}
                Load older events
              </Button>
            </div>
          )}
        </>
      )}
    </section>
  )
}

type Tone = "success" | "danger" | "warning" | "neutral"

function AlertEventRow({
  event,
  hasEvidence,
  renderEvidence,
}: {
  event: AlertEvent
  hasEvidence?: (evaluationID?: string) => boolean
  renderEvidence?: (evaluationID?: string) => React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  const { label, tone, icon } = describeEvent(event)
  const narrative = eventNarrative(event)
  const evaluationID = event.evaluationID ?? undefined
  // Expandable only when this event's evaluation has retained evidence to show.
  const expandable = !!evaluationID && (hasEvidence?.(evaluationID) ?? false)

  const header = (
    <>
      <div className="flex flex-wrap items-start gap-2">
        {expandable && (
          <ChevronDown
            className={cn(
              "mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
              !open && "-rotate-90"
            )}
          />
        )}
        <div className="min-w-0">
          <div className="text-sm font-medium">{label}</div>
          {event.prevStatus && (
            <div className="mt-0.5 text-xs text-muted-foreground">
              {event.prevStatus} → {event.newStatus}
            </div>
          )}
        </div>
        <time
          className="ml-auto shrink-0 text-xs text-muted-foreground"
          dateTime={event.at}
          title={formatDateTime(event.at)}
        >
          {formatRelative(event.at)}
        </time>
      </div>

      {narrative && (
        <p className="mt-1.5 text-xs break-words text-muted-foreground">
          {narrative}
        </p>
      )}

      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[10px] text-muted-foreground">
        {event.transactionId && (
          <span title="Control-ledger transaction id of this write (in the signed audit chain)">
            tx {event.transactionId}
          </span>
        )}
        {event.evaluationID && (
          <span title={event.evaluationID}>
            eval {event.evaluationID.slice(0, 8)}
          </span>
        )}
        {!event.notify && (
          <span className="rounded-full border px-1.5 py-px" title="Recorded for audit but not published to the message bus">
            suppressed
          </span>
        )}
      </div>
    </>
  )

  return (
    <li className="relative min-w-0">
      <TimelineDot tone={tone} icon={icon} />
      <Card className="min-w-0 overflow-hidden">
        {expandable ? (
          <button
            type="button"
            onClick={() => setOpen((v) => !v)}
            aria-expanded={open}
            className="w-full p-3 text-left transition-colors hover:bg-muted/25"
          >
            {header}
          </button>
        ) : (
          <div className="p-3">{header}</div>
        )}
        {expandable && open && (
          <div className="border-t bg-muted/15 p-3">
            {renderEvidence?.(evaluationID)}
          </div>
        )}
      </Card>
    </li>
  )
}

function describeEvent(event: AlertEvent): {
  label: string
  tone: Tone
  icon: React.ReactNode
} {
  switch (event.type) {
    case "fail":
      if (event.isReopen)
        return { label: "Alert reopened", tone: "danger", icon: <CircleAlert /> }
      if (!event.prevStatus)
        return { label: "Alert opened", tone: "danger", icon: <CircleAlert /> }
      return {
        label: "Discrepancy observed again",
        tone: "danger",
        icon: <XCircle />,
      }
    case "pass":
      return {
        label: "Resolved automatically",
        tone: "success",
        icon: <ShieldCheck />,
      }
    case "ack":
      return { label: "Acknowledged", tone: "neutral", icon: <CheckCircle2 /> }
    case "resolve":
      return {
        label: "Resolved after booking",
        tone: "success",
        icon: <ShieldCheck />,
      }
    case "accept":
      return {
        label: "Accepted by business",
        tone: "success",
        icon: <CheckCircle2 />,
      }
    case "snooze":
      return { label: "Snoozed", tone: "warning", icon: <BellOff /> }
    case "unsnooze":
      return { label: "Unsnoozed", tone: "neutral", icon: <Bell /> }
    default:
      return { label: event.type, tone: "neutral", icon: <CircleAlert /> }
  }
}

// eventNarrative pulls the human-readable detail out of the transition payload:
// the actor + note on a manual action, booking refs on a resolution, and the
// occurrence count on a recurrence.
function eventNarrative(event: AlertEvent): string | undefined {
  const payload = event.payload
  if (!payload) return undefined
  const nested = [payload.ack, payload.resolution, payload.snooze].find(isRecord)
  const actor =
    typeof nested?.by === "string"
      ? nested.by
      : typeof payload.by === "string"
        ? payload.by
        : undefined
  const note = typeof nested?.note === "string" ? nested.note : undefined
  const count =
    typeof payload.occurrenceCount === "number"
      ? payload.occurrenceCount
      : undefined
  const refs = Array.isArray(nested?.transactionRefs)
    ? nested.transactionRefs.filter((v): v is string => typeof v === "string")
    : []
  return (
    [
      actor ? `by ${actor}` : undefined,
      count !== undefined ? `occurrence ${count}` : undefined,
      refs.length > 0 ? `booking refs ${refs.join(", ")}` : undefined,
      note,
    ]
      .filter(Boolean)
      .join(" · ") || undefined
  )
}

function TimelineDot({ icon, tone }: { icon: React.ReactNode; tone: Tone }) {
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}
