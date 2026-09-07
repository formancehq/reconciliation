import { Card } from "@/components/ui/card"
import { evidenceLabel, type NamedSourceV2, type RuleV2 } from "@/lib/recon"
import { describeRuleV2, effectiveSourceKind, sourceName } from "@/lib/recon/v2"

export function V2RulePresentation({
  rule,
  compact = false,
  sourcesFirst = false,
}: {
  rule: RuleV2
  compact?: boolean
  sourcesFirst?: boolean
}) {
  // Not every V2 template carries a `sources` array: stale_holds reads a single
  // named hold set, so surface it as the one source card rather than nothing.
  const sources: NamedSourceV2[] =
    rule.templateKind === "stale_holds"
      ? [rule.templateSpec.source]
      : ((rule.templateSpec as { sources?: NamedSourceV2[] }).sources ?? [])
  const sourceGrid =
    sources.length > 0 ? (
      <div
        className={
          compact
            ? "grid min-w-0 gap-2 sm:grid-cols-2"
            : "grid min-w-0 gap-3 md:grid-cols-2 xl:grid-cols-3"
        }
      >
        {sources.map((source) => (
          <NamedSourceCard key={source.id} source={source} compact={compact} />
        ))}
      </div>
    ) : null
  // A kind describeRuleV2 doesn't describe yields no invariant text, so skip
  // the box rather than render an empty one.
  const invariant = describeRuleV2(rule)
  const operation = !invariant ? null : (
    <div className="rounded-md border bg-muted/25 px-3 py-2">
      <div className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
        Invariant
      </div>
      <div className="mt-1 text-sm font-medium break-words">{invariant}</div>
      {rule.templateKind === "exchange_rate_bounds" && (
        <div className="mt-1 text-xs text-muted-foreground">
          Convention: quote asset major units per base asset major unit
        </div>
      )}
      {rule.templateKind === "source_consensus" && (
        <div className="mt-1 text-xs text-muted-foreground">
          Every source participates symmetrically; there is no baseline source.
        </div>
      )}
      {rule.templateKind === "stale_holds" && (
        <div className="mt-1 text-xs text-muted-foreground">
          A hold is one account holding funds past the deadline on its own
          metadata. Released holds — zero balance — are ignored.
        </div>
      )}
    </div>
  )
  return (
    <div className="min-w-0 space-y-3">
      {sourcesFirst ? sourceGrid : operation}
      {sourcesFirst ? operation : sourceGrid}
    </div>
  )
}

export function NamedSourceCard({
  source,
  compact = false,
}: {
  source: NamedSourceV2
  compact?: boolean
}) {
  const query = safeDisplay(source.query)
  return (
    <Card className="min-w-0 overflow-hidden p-3">
      <div className="flex min-w-0 items-baseline justify-between gap-2">
        <span
          className="truncate text-sm font-medium"
          title={sourceName(source)}
        >
          {sourceName(source)}
        </span>
        <span className="shrink-0 rounded-full border px-2 py-0.5 font-mono text-[10px]">
          {source.asset}
        </span>
      </div>
      {source.label && (
        <div
          className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground"
          title={source.id}
        >
          id: {source.id}
        </div>
      )}
      <dl
        className={`mt-2 grid gap-x-2 gap-y-1 text-xs ${compact ? "grid-cols-[auto_minmax(0,1fr)]" : "grid-cols-[5rem_minmax(0,1fr)]"}`}
      >
        <dt className="text-muted-foreground">Kind</dt>
        <dd>
          {effectiveSourceKind(source) === "account_metadata"
            ? "Account metadata"
            : "Ledger balance"}
        </dd>
        <dt className="text-muted-foreground">Ledger</dt>
        <dd className="truncate font-mono" title={source.ledger}>
          {source.ledger}
        </dd>
        <dt className="text-muted-foreground">Query</dt>
        <dd className="font-mono text-[11px] break-all" title={query}>
          {query}
        </dd>
        {effectiveSourceKind(source) === "account_metadata" && (
          <>
            <dt className="text-muted-foreground">Metadata</dt>
            <dd className="font-mono text-[11px] break-all">
              {source.kind === "account_metadata" ? source.metadataKey : "—"}
            </dd>
          </>
        )}
      </dl>
    </Card>
  )
}

function safeDisplay(value: unknown): string {
  if (typeof value === "string") return value
  try {
    return JSON.stringify(value)
  } catch {
    return evidenceLabel("unavailable")
  }
}
