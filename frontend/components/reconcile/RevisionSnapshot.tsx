import { Braces, CalendarClock, Code2, Settings2 } from "lucide-react"
import {
  PERIOD_TYPE_META,
  describeAnyRule,
  formatDateTime,
  isRuleV2,
  ruleFromActivitySnapshot,
  templateLabel,
} from "@/lib/recon"
import { SourceParityComparison } from "./SourceParityComparison"
import { V2RulePresentation } from "./V2RulePresentation"

export function RevisionSnapshot({
  snapshot,
  contractVersion,
}: {
  snapshot: Record<string, unknown>
  contractVersion: 1 | 2
}) {
  const rule = ruleFromActivitySnapshot(snapshot, contractVersion)

  if (!rule) {
    return <RawSnapshot snapshot={snapshot} initiallyOpen />
  }

  const schedule = rule.schedule
    ? rule.schedule.kind === "cron"
      ? `${rule.schedule.expr ?? "Cron schedule"} · ${rule.schedule.tz ?? "UTC"}`
      : "On demand"
    : "Not configured"

  return (
    <div className="min-w-0 space-y-3 border-t bg-background p-3">
      <div className="flex flex-wrap items-start gap-3 rounded-md border bg-muted/20 p-3">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground">
          <Settings2 className="h-4 w-4" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h4 className="text-sm font-semibold break-words">{rule.name}</h4>
            <span className="rounded-full border bg-background px-2 py-0.5 text-[10px] font-medium">
              {templateLabel(rule.templateKind)}
            </span>
            {isRuleV2(rule) && (
              <span className="rounded-full border bg-background px-2 py-0.5 font-mono text-[10px]">
                V2
              </span>
            )}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            Complete configuration captured with this revision.
          </p>
        </div>
      </div>

      <dl className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <SnapshotFact label="State" value={rule.enabled ? "Enabled" : "Disabled"} />
        <SnapshotFact label="Severity" value={rule.severity} />
        <SnapshotFact
          label="Period type"
          value={PERIOD_TYPE_META[rule.periodType]?.label ?? rule.periodType}
        />
        <SnapshotFact label="Schedule" value={schedule} />
      </dl>

      <div className="min-w-0 space-y-3 rounded-md border p-3">
        <div className="flex items-center gap-2">
          <Braces className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          <h4 className="text-xs font-semibold">Sources and invariant</h4>
        </div>
        {isRuleV2(rule) ? (
          <V2RulePresentation rule={rule} compact sourcesFirst />
        ) : rule.templateKind === "source_parity" ? (
          <SourceParityComparison spec={rule.templateSpec} density="compact" />
        ) : (
          <p className="rounded-md bg-muted/30 px-3 py-2 text-sm break-words">
            {describeAnyRule(rule)}
          </p>
        )}
      </div>

      <div className="grid gap-2 sm:grid-cols-2">
        <SnapshotTime label="Created" value={rule.createdAt} />
        <SnapshotTime label="Updated" value={rule.updatedAt} />
      </div>

      {rule.compiledCEL && (
        <details className="rounded-md border text-xs">
          <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 font-medium select-none">
            <Code2 className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
            Implementation details
          </summary>
          <pre className="max-h-72 overflow-auto border-t bg-muted/20 px-3 py-2 font-mono text-[11px] break-all whitespace-pre-wrap text-muted-foreground">
            {rule.compiledCEL}
          </pre>
        </details>
      )}

      <RawSnapshot snapshot={snapshot} />
    </div>
  )
}

function SnapshotFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-md border bg-background px-3 py-2">
      <dt className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="mt-0.5 truncate text-xs font-medium capitalize" title={value}>
        {value}
      </dd>
    </div>
  )
}

function SnapshotTime({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 items-start gap-2 rounded-md border px-3 py-2">
      <CalendarClock className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0">
        <div className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          {label}
        </div>
        <time
          className="text-xs break-words"
          dateTime={value}
          title={formatDateTime(value)}
        >
          {formatDateTime(value)}
        </time>
      </div>
    </div>
  )
}

function RawSnapshot({
  snapshot,
  initiallyOpen = false,
}: {
  snapshot: Record<string, unknown>
  initiallyOpen?: boolean
}) {
  return (
    <details className="rounded-md border text-xs" open={initiallyOpen}>
      <summary className="cursor-pointer px-3 py-2 font-medium select-none">
        Raw snapshot · JSON
      </summary>
      <pre className="max-h-96 overflow-auto border-t bg-muted/20 px-3 py-2 font-mono text-[11px] break-all whitespace-pre-wrap text-muted-foreground">
        {JSON.stringify(snapshot, null, 2)}
      </pre>
    </details>
  )
}
