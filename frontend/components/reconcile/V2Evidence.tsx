import { AlertTriangle, CheckCircle2, XCircle } from "lucide-react"
import { Card } from "@/components/ui/card"
import type {
  BalanceEquationEvidenceSource,
  CoveragePortfolio,
  EvidenceSource,
  Evidence,
  Rational,
  StaleHoldEvidence,
} from "@/lib/recon"
import { sourceName } from "@/lib/recon/v2"

export function isEvidence(value: unknown): value is Evidence {
  if (
    !isRecord(value) ||
    value.schemaVersion !== 2 ||
    typeof value.operation !== "string"
  )
    return false
  if (value.operation === "balance_equation") {
    return (
      Array.isArray(value.sources) &&
      value.sources.every(isEquationSource) &&
      strings(value, [
        "asset",
        "residual",
        "absoluteResidual",
        "tolerance",
        "compiledCEL",
      ])
    )
  }
  if (value.operation === "exchange_rate_bounds") {
    return (
      isEvidenceSource(value.base) &&
      isEvidenceSource(value.quote) &&
      isBounds(value.effectiveBounds) &&
      optionalRational(value.observedRate) &&
      strings(value, ["compiledCEL"])
    )
  }
  if (value.operation === "source_consensus") {
    return (
      Array.isArray(value.sources) &&
      value.sources.every(isEvidenceSource) &&
      Array.isArray(value.missingSources) &&
      value.missingSources.every((source) => typeof source === "string") &&
      strings(value, [
        "asset",
        "minimumSource",
        "minimumBalance",
        "maximumSource",
        "maximumBalance",
        "spread",
        "tolerance",
        "compiledCEL",
      ])
    )
  }
  if (value.operation === "coverage_ratio_bounds") {
    return (
      isPortfolio(value.numerator) &&
      isPortfolio(value.denominator) &&
      isBounds(value.effectiveBounds) &&
      optionalRational(value.observedRatio) &&
      strings(value, ["asset", "compiledCEL"])
    )
  }
  if (value.operation === "balance_bounds") {
    return (
      isEvidenceSource(value.source) &&
      isRecord(value.effectiveBounds) &&
      strings(value, ["asset", "excursion", "compiledCEL"])
    )
  }
  if (value.operation === "stale_holds") {
    if (
      !strings(value, ["mode", "asset", "sourceId", "evaluatedAt", "compiledCEL"])
    )
      return false
    // One flagged hold, or the scan summary behind an outcome.
    return "hold" in value
      ? strings(value, ["hold", "amount", "basis", "deadline"])
      : typeof value.holdsMatched === "number" &&
          typeof value.holdsFlagged === "number" &&
          strings(value, ["deadlineOnOrBefore", "amountFlagged"])
  }
  return false
}

export function V2Evidence({
  evidence,
  compact = false,
  passed,
}: {
  evidence: unknown
  compact?: boolean
  passed?: boolean
}) {
  if (!isEvidence(evidence))
    return <StructuredEvidenceFallback evidence={evidence} />
  const outcomePassed = passed ?? inferEvidencePassed(evidence)
  switch (evidence.operation) {
    case "balance_equation":
      return (
        <BalanceEquationEvidence
          evidence={evidence}
          passed={outcomePassed}
          compact={compact}
        />
      )
    case "exchange_rate_bounds":
      return (
        <ExchangeRateEvidence
          evidence={evidence}
          passed={outcomePassed}
          compact={compact}
        />
      )
    case "source_consensus":
      return (
        <SourceConsensusEvidence
          evidence={evidence}
          passed={outcomePassed}
          compact={compact}
        />
      )
    case "coverage_ratio_bounds":
      return (
        <CoverageRatioEvidence
          evidence={evidence}
          passed={outcomePassed}
          compact={compact}
        />
      )
    case "balance_bounds":
      return (
        <BalanceBoundsEvidence
          evidence={evidence}
          passed={outcomePassed}
          compact={compact}
        />
      )
    case "stale_holds":
      return (
        <StaleHoldsEvidence
          evidence={evidence}
          passed={outcomePassed}
          compact={compact}
        />
      )
  }
}

type EvidenceOf<Operation extends Evidence["operation"]> = Extract<
  Evidence,
  { operation: Operation }
>

interface OperationEvidenceProps<Operation extends Evidence["operation"]> {
  evidence: EvidenceOf<Operation>
  passed: boolean
  compact: boolean
}

export function BalanceEquationEvidence({
  evidence,
  passed,
  compact,
}: OperationEvidenceProps<"balance_equation">) {
  return (
    <EvidenceShell
      title="Balance equation"
      summary={`|${evidence.residual}| = ${evidence.absoluteResidual}; tolerance ${evidence.tolerance} ${evidence.asset}`}
      passed={passed}
      verdict={`The signed equation ${passed ? "balances" : "does not balance"} within tolerance.`}
      compiledCEL={evidence.compiledCEL}
      compact={compact}
    >
      <SourceTable sources={evidence.sources} equation compact={compact} />
      <MetricGrid
        metrics={[
          ["Residual", evidence.residual],
          ["Absolute residual", evidence.absoluteResidual],
          ["Tolerance", evidence.tolerance],
        ]}
      />
    </EvidenceShell>
  )
}

export function ExchangeRateEvidence({
  evidence,
  passed,
  compact,
}: OperationEvidenceProps<"exchange_rate_bounds">) {
  return (
    <EvidenceShell
      title="Exchange-rate bounds"
      summary="quote major units per base major unit"
      passed={passed}
      verdict={
        evidence.undefinedReason === "base_balance_zero"
          ? "Failed control: the quote-per-base rate is undefined."
          : `${passed ? "Pass" : "Fail"}: the exact quote-per-base rate ${passed ? "is" : "is not"} inside the inclusive bounds.`
      }
      compiledCEL={evidence.compiledCEL}
      compact={compact}
    >
      <div className="grid gap-2 sm:grid-cols-2">
        <SnapshotCard role="Base source" source={evidence.base} />
        <SnapshotCard role="Quote source" source={evidence.quote} />
      </div>
      {evidence.undefinedReason === "base_balance_zero" ? (
        <ControlFailure>
          Observed rate is undefined because the base source balance is zero.
        </ControlFailure>
      ) : (
        <MetricGrid
          metrics={[
            ["Observed exact rate", exactRational(evidence.observedRate)],
            ["Inclusive minimum", evidence.effectiveBounds.min],
            ["Inclusive maximum", evidence.effectiveBounds.max],
          ]}
        />
      )}
    </EvidenceShell>
  )
}

export function SourceConsensusEvidence({
  evidence,
  passed,
  compact,
}: OperationEvidenceProps<"source_consensus">) {
  const missing = new Set(evidence.missingSources)
  return (
    <EvidenceShell
      title="Source consensus"
      summary={`maximum − minimum = ${evidence.spread} ≤ ${evidence.tolerance}`}
      passed={passed}
      verdict={`${passed ? "Pass" : "Fail"}: ${passed ? "every source is present and the widest spread is within tolerance" : "the all-sources consensus control is not satisfied"}.`}
      compiledCEL={evidence.compiledCEL}
      compact={compact}
    >
      {evidence.missingSources.length > 0 && (
        <ControlFailure>
          Missing sources:{" "}
          {evidence.missingSources
            .map((id) => displaySourceId(evidence.sources, id))
            .join(", ")}
          . Missing records fail the control and are not treated as healthy
          zeroes.
        </ControlFailure>
      )}
      <SourceTable
        sources={evidence.sources}
        missing={missing}
        minimumSource={evidence.minimumSource}
        maximumSource={evidence.maximumSource}
        compact={compact}
      />
      <MetricGrid
        metrics={[
          [
            "Minimum source",
            displaySourceId(evidence.sources, evidence.minimumSource),
          ],
          ["Minimum balance", evidence.minimumBalance],
          [
            "Maximum source",
            displaySourceId(evidence.sources, evidence.maximumSource),
          ],
          ["Maximum balance", evidence.maximumBalance],
          ["Spread", evidence.spread],
          ["Tolerance", evidence.tolerance],
        ]}
      />
    </EvidenceShell>
  )
}

export function CoverageRatioEvidence({
  evidence,
  passed,
  compact,
}: OperationEvidenceProps<"coverage_ratio_bounds">) {
  return (
    <EvidenceShell
      title="Coverage-ratio bounds"
      summary={evidence.asset}
      passed={passed}
      verdict={
        evidence.undefinedReason === "denominator_total_zero"
          ? "Failed control: the coverage ratio is undefined."
          : `${passed ? "Pass" : "Fail"}: the exact portfolio ratio ${passed ? "is" : "is not"} inside the inclusive bounds.`
      }
      compiledCEL={evidence.compiledCEL}
      compact={compact}
    >
      <div className="grid gap-3 lg:grid-cols-2">
        <Portfolio
          title="Numerator portfolio"
          portfolio={evidence.numerator}
          compact={compact}
        />
        <Portfolio
          title="Denominator portfolio"
          portfolio={evidence.denominator}
          compact={compact}
        />
      </div>
      {evidence.undefinedReason === "denominator_total_zero" ? (
        <ControlFailure>
          Observed ratio is undefined because the denominator portfolio totals
          zero.
        </ControlFailure>
      ) : (
        <MetricGrid
          metrics={[
            ["Observed exact ratio", exactRational(evidence.observedRatio)],
            ["Inclusive minimum", evidence.effectiveBounds.min],
            ["Inclusive maximum", evidence.effectiveBounds.max],
          ]}
        />
      )}
    </EvidenceShell>
  )
}

function EvidenceShell({
  title,
  summary,
  passed,
  verdict,
  compiledCEL,
  compact,
  children,
}: {
  title: string
  summary: string
  passed: boolean
  verdict: string
  compiledCEL: string
  compact: boolean
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <div className="text-sm font-medium">{title}</div>
        <div className="font-mono text-xs break-words text-muted-foreground">
          {summary}
        </div>
      </div>
      <div
        role="status"
        className={`flex items-start gap-2 rounded-md border px-3 py-2 text-sm ${
          passed
            ? "border-green-foreground/30 bg-green-background text-green-foreground"
            : "border-destructive-foreground/30 border-l-2 border-l-destructive-foreground bg-destructive text-destructive-foreground"
        }`}
      >
        {passed ? (
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
        ) : (
          <XCircle className="mt-0.5 h-4 w-4 shrink-0" />
        )}
        <div className="min-w-0">
          <div className="font-medium">
            {passed ? "Control passed" : "Control failed"}
          </div>
          <div className="text-xs/5 opacity-90">{verdict}</div>
        </div>
      </div>
      {children}
      {!compact && (
        <>
          <p className="text-[10px] text-muted-foreground">
            All balances, contributions, and exact rational parts are displayed
            as received; no JavaScript floating-point conversion is used.
          </p>
          <details className="rounded-md border bg-muted/20 text-xs">
            <summary className="cursor-pointer px-3 py-2 font-medium text-muted-foreground select-none hover:text-foreground">
              Technical details (compiled CEL)
            </summary>
            <pre className="max-h-40 overflow-auto border-t px-3 py-2 font-mono text-[11px] break-all whitespace-pre-wrap text-muted-foreground">
              {compiledCEL}
            </pre>
          </details>
        </>
      )}
    </div>
  )
}

function SourceTable({
  sources,
  equation = false,
  missing = new Set<string>(),
  minimumSource,
  maximumSource,
  compact = false,
}: {
  sources: Array<EvidenceSource | BalanceEquationEvidenceSource>
  equation?: boolean
  missing?: Set<string>
  minimumSource?: string
  maximumSource?: string
  compact?: boolean
}) {
  if (!Array.isArray(sources))
    return <ControlFailure>Malformed source evidence.</ControlFailure>
  const sourceRole = (id: string) =>
    id === minimumSource && id === maximumSource
      ? "Minimum and maximum"
      : id === minimumSource
        ? "Minimum"
        : id === maximumSource
          ? "Maximum"
          : undefined
  return (
    <div data-testid="v2-responsive-source-table" className="min-w-0">
      <div className="grid min-w-0 gap-2 md:hidden">
        {sources.map((source) => (
          <SnapshotCard
            key={source.id}
            source={source}
            role={sourceRole(source.id)}
            missing={missing.has(source.id)}
            equation={equation}
          />
        ))}
      </div>
      <div className="hidden min-w-0 overflow-x-auto rounded-md border md:block">
        <table className="w-full min-w-[42rem] text-left text-xs">
          <thead className="bg-muted/35 text-[10px] tracking-wide text-muted-foreground uppercase">
            <tr>
              <th className="px-3 py-2 font-medium">Source</th>
              <th className="px-3 py-2 font-medium">Balance</th>
              {equation && (
                <th className="px-3 py-2 font-medium">Coefficient</th>
              )}
              {equation && (
                <th className="px-3 py-2 font-medium">Contribution</th>
              )}
              <th className="px-3 py-2 font-medium">Presence</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {sources.map((source) => {
              const absent = missing.has(source.id) || !source.present
              const coefficient =
                equation && "coefficient" in source
                  ? source.coefficient
                  : undefined
              const contribution =
                equation && "contribution" in source
                  ? source.contribution
                  : undefined
              return (
                <tr
                  key={source.id}
                  className={absent ? "bg-destructive/5" : undefined}
                >
                  <td className="min-w-44 px-3 py-2">
                    <div className="font-medium">{sourceName(source)}</div>
                    {source.label && (
                      <div className="font-mono text-[10px] text-muted-foreground">
                        id: {source.id}
                      </div>
                    )}
                    {sourceRole(source.id) && (
                      <div className="mt-0.5 text-[10px] font-medium text-primary">
                        {sourceRole(source.id)} source
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-2 font-mono break-all">
                    {source.balance}
                    <span className="ml-1 text-[10px] text-muted-foreground">
                      {source.asset}
                    </span>
                  </td>
                  {equation && (
                    <td className="px-3 py-2 font-mono">{coefficient}</td>
                  )}
                  {equation && (
                    <td className="px-3 py-2 font-mono break-all">
                      {contribution}
                    </td>
                  )}
                  <td className="px-3 py-2">
                    <Presence present={!absent} />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {compact && (
        <span className="sr-only">Responsive source evidence table</span>
      )}
    </div>
  )
}

function SnapshotCard({
  source,
  role,
  missing = false,
  equation = false,
}: {
  source: EvidenceSource | BalanceEquationEvidenceSource
  role?: string
  missing?: boolean
  equation?: boolean
}) {
  const contribution =
    equation && "contribution" in source ? source.contribution : undefined
  const coefficient =
    equation && "coefficient" in source ? source.coefficient : undefined
  return (
    <Card
      className={`min-w-0 p-3 ${missing || !source.present ? "border-destructive/50" : ""}`}
    >
      {role && (
        <div className="mb-1 text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          {role}
        </div>
      )}
      <div className="flex min-w-0 items-center gap-2">
        {source.present && !missing ? (
          <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-green-foreground" />
        ) : (
          <XCircle className="h-3.5 w-3.5 shrink-0 text-destructive" />
        )}
        <span
          className="truncate text-sm font-medium"
          title={sourceName(source)}
        >
          {sourceName(source)}
        </span>
        <span className="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">
          {source.asset}
        </span>
      </div>
      {source.label && (
        <div className="truncate pl-5 font-mono text-[10px] text-muted-foreground">
          id: {source.id}
        </div>
      )}
      <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">Kind</dt>
        <dd>
          {source.kind === "account_metadata"
            ? "Account metadata"
            : "Ledger balance"}
        </dd>
        <dt className="text-muted-foreground">Balance</dt>
        <dd className="font-mono break-all">{source.balance}</dd>
        <dt className="text-muted-foreground">Presence</dt>
        <dd>
          <Presence present={source.present && !missing} />
        </dd>
        {coefficient !== undefined && (
          <>
            <dt className="text-muted-foreground">Coefficient</dt>
            <dd className="font-mono">{coefficient}</dd>
          </>
        )}
        {contribution !== undefined && (
          <>
            <dt className="text-muted-foreground">Contribution</dt>
            <dd className="font-mono break-all">{contribution}</dd>
          </>
        )}
      </dl>
    </Card>
  )
}

function Portfolio({
  title,
  portfolio,
  compact,
}: {
  title: string
  portfolio: CoveragePortfolio
  compact: boolean
}) {
  return (
    <div className="min-w-0 rounded-md border p-3">
      <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
        <span className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {title}
        </span>
        <span className="font-mono text-sm font-medium">
          Total {portfolio.total}
        </span>
      </div>
      <SourceTable sources={portfolio.sources} equation compact={compact} />
    </div>
  )
}

function Presence({ present }: { present: boolean }) {
  return present ? (
    <span className="inline-flex items-center gap-1 text-green-foreground">
      <CheckCircle2 className="h-3.5 w-3.5" /> Observed
    </span>
  ) : (
    <span className="inline-flex items-center gap-1 font-medium text-destructive-foreground">
      <XCircle className="h-3.5 w-3.5" /> Missing (not zero)
    </span>
  )
}

export function BalanceBoundsEvidence({
  evidence,
  passed,
  compact,
}: OperationEvidenceProps<"balance_bounds">) {
  const { min, max } = evidence.effectiveBounds
  const range =
    min !== undefined && max !== undefined
      ? `${min} ≤ balance ≤ ${max}`
      : min !== undefined
        ? `balance ≥ ${min}`
        : `balance ≤ ${max}`
  const breached = evidence.breachedBound

  return (
    <EvidenceShell
      title="Balance bounds"
      summary={`${evidence.source.balance} ${evidence.asset} · ${range}`}
      passed={passed}
      verdict={
        passed
          ? `Pass: the balance sits inside its ${min !== undefined && max !== undefined ? "range" : "limit"}.`
          : `Fail: ${breached === "min" ? "below the minimum" : "above the maximum"} by ${absolute(evidence.excursion)} ${evidence.asset}.`
      }
      compiledCEL={evidence.compiledCEL}
      compact={compact}
    >
      {!evidence.source.present && (
        <ControlFailure>
          The account set holds no {evidence.asset} at all, so the balance reads
          zero. A minimum is still checked against it — that is the point of
          naming the asset on the rule rather than discovering it.
        </ControlFailure>
      )}
      <MetricGrid
        metrics={[
          ["Balance", `${evidence.source.balance} ${evidence.asset}`],
          ["Minimum", min ?? "unbounded"],
          ["Maximum", max ?? "unbounded"],
          ["Outside by", passed ? "—" : `${absolute(evidence.excursion)} ${evidence.asset}`],
          ["Source", displaySourceId([evidence.source], evidence.source.id)],
          ["Holds this asset", evidence.source.present ? "yes" : "no"],
        ]}
      />
    </EvidenceShell>
  )
}

/** Drop a leading minus: the direction is already named in the verdict. */
function absolute(value: string): string {
  return value.startsWith("-") ? value.slice(1) : value
}

/**
 * stale_holds evidence comes in two shapes from the same template: one flagged
 * hold (what an alert carries in per_hold scope) or the scan behind an outcome
 * (aggregate scope, and a clean per_hold run). They render differently because
 * they answer different questions — "which hold, how overdue" versus "how much
 * is trapped in total".
 */
export function StaleHoldsEvidence({
  evidence,
  passed,
  compact,
}: OperationEvidenceProps<"stale_holds">) {
  const approaching = evidence.mode === "approaching"
  if ("hold" in evidence) {
    const elapsed = approaching
      ? formatSeconds(evidence.dueInSeconds)
      : formatSeconds(evidence.overdueSeconds)
    const identity = evidence.identity ?? {}
    const identityEntries = Object.entries(identity)
    return (
      <EvidenceShell
        title={approaching ? "Hold approaching its deadline" : "Stale hold"}
        summary={`${describeHold(evidence)} · ${evidence.amount} ${evidence.asset}`}
        passed={passed}
        verdict={
          approaching
            ? `Due in ${elapsed}: this hold reaches its deadline inside the rule's warning window.`
            : `Overdue by ${elapsed}: funds are still held past the deadline on this account.`
        }
        compiledCEL={evidence.compiledCEL}
        compact={compact}
      >
        {identityEntries.length > 0 && (
          <MetricGrid
            metrics={identityEntries.map(
              ([key, value]) => [key, value] as [string, string]
            )}
          />
        )}
        <MetricGrid
          metrics={[
            ["Hold account", evidence.hold],
            ["Amount held", `${evidence.amount} ${evidence.asset}`],
            ["Deadline", evidence.deadline],
            [
              "Dated from",
              evidence.basis === "expiry"
                ? "the expiry on the hold"
                : "creation + maximum age",
            ],
            [approaching ? "Due in" : "Overdue by", elapsed],
            ["Evaluated at", evidence.evaluatedAt],
          ]}
        />
      </EvidenceShell>
    )
  }

  const window = approaching
    ? `deadline after ${evidence.deadlineAfter ?? "now"} and on or before ${evidence.deadlineOnOrBefore}`
    : `deadline on or before ${evidence.deadlineOnOrBefore}`
  return (
    <EvidenceShell
      title={approaching ? "Holds approaching their deadline" : "Stale holds"}
      summary={`${evidence.holdsFlagged} of ${evidence.holdsMatched} matched holds · ${evidence.amountFlagged} ${evidence.asset} held`}
      passed={passed}
      verdict={
        passed
          ? `Pass: no hold matched ${window}.`
          : `Fail: ${evidence.holdsFlagged} hold${evidence.holdsFlagged === 1 ? "" : "s"} held ${evidence.amountFlagged} ${evidence.asset} past ${approaching ? "the warning window" : "the deadline"}.`
      }
      compiledCEL={evidence.compiledCEL}
      compact={compact}
    >
      {evidence.holdsMatched >= evidence.holdsBudget && (
        <ControlFailure>
          The scan reached its budget of {evidence.holdsBudget} holds. Narrow the
          rule&apos;s account query — or raise its limit — so the check sees the
          whole hold set.
        </ControlFailure>
      )}
      <MetricGrid
        metrics={[
          ["Holds flagged", String(evidence.holdsFlagged)],
          ["Amount held", `${evidence.amountFlagged} ${evidence.asset}`],
          ["Oldest deadline", evidence.oldestDeadline ?? "—"],
          ["Holds matched", String(evidence.holdsMatched)],
          ["Released, ignored", String(evidence.holdsReleased)],
          ["Window", window],
        ]}
      />
      {evidence.holds && evidence.holds.length > 0 && (
        <StaleHoldSample
          holds={evidence.holds}
          asset={evidence.asset}
          sampled={evidence.holdsSampled ?? evidence.holds.length}
          total={evidence.holdsFlagged}
          approaching={approaching}
        />
      )}
    </EvidenceShell>
  )
}

function StaleHoldSample({
  holds,
  asset,
  sampled,
  total,
  approaching,
}: {
  holds: StaleHoldEvidence[]
  asset: string
  sampled: number
  total: number
  approaching: boolean
}) {
  return (
    <div className="min-w-0 space-y-2">
      <div className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
        {sampled < total
          ? `Oldest ${sampled} of ${total} holds`
          : `All ${total} holds`}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-0 text-sm">
          <tbody>
            {holds.map((hold) => (
              <tr key={hold.hold} className="border-b last:border-b-0">
                <td className="py-1 pr-3 font-medium break-all">
                  {describeHold(hold)}
                </td>
                <td className="py-1 pr-3 whitespace-nowrap tabular-nums">
                  {hold.amount} {asset}
                </td>
                <td className="py-1 whitespace-nowrap text-muted-foreground">
                  {approaching
                    ? `due in ${formatSeconds(hold.dueInSeconds)}`
                    : `overdue by ${formatSeconds(hold.overdueSeconds)}`}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

/** Prefer the operator's own identifiers over the ledger address when the rule supplies them. */
function describeHold(hold: StaleHoldEvidence): string {
  const identity = Object.values(hold.identity ?? {}).filter(Boolean)
  return identity.length > 0 ? identity.join(" · ") : hold.hold
}

/** Whole units, largest first: "6h 12m", "3d 4h", "45s". */
function formatSeconds(seconds: number | undefined): string {
  if (seconds === undefined || !Number.isFinite(seconds)) return "—"
  const total = Math.max(0, Math.floor(seconds))
  if (total < 60) return `${total}s`
  const days = Math.floor(total / 86_400)
  const hours = Math.floor((total % 86_400) / 3_600)
  const minutes = Math.floor((total % 3_600) / 60)
  if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`
  if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`
  return `${minutes}m`
}

function MetricGrid({ metrics }: { metrics: Array<[string, string]> }) {
  return (
    <dl className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {metrics.map(([label, value]) => (
        <div
          key={label}
          className="min-w-0 rounded-md border bg-muted/20 px-3 py-2"
        >
          <dt className="text-[10px] tracking-wide text-muted-foreground uppercase">
            {label}
          </dt>
          <dd className="font-mono text-sm font-medium break-all">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function ControlFailure({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-destructive-foreground/30 border-l-2 border-l-destructive-foreground bg-destructive px-3 py-2 text-sm font-medium text-destructive-foreground">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      {children}
    </div>
  )
}

function exactRational(value: Rational | undefined): string {
  return value ? `${value.numerator} / ${value.denominator}` : "—"
}

/** Infer a display verdict only when an enclosing Outcome did not provide it. */
export function inferEvidencePassed(evidence: Evidence): boolean {
  try {
    if (evidence.operation === "balance_equation") {
      return (
        evidence.sources.every((source) => source.present) &&
        BigInt(evidence.absoluteResidual) <= BigInt(evidence.tolerance)
      )
    }
    if (evidence.operation === "source_consensus") {
      return (
        evidence.missingSources.length === 0 &&
        evidence.sources.every((source) => source.present) &&
        BigInt(evidence.spread) <= BigInt(evidence.tolerance)
      )
    }
    if (evidence.operation === "exchange_rate_bounds") {
      return (
        evidence.base.present &&
        evidence.quote.present &&
        evidence.undefinedReason === undefined &&
        rationalWithinBounds(evidence.observedRate, evidence.effectiveBounds)
      )
    }
    if (evidence.operation === "balance_bounds") {
      return evidence.breachedBound === undefined
    }
    if (evidence.operation === "stale_holds") {
      // A per-hold outcome exists only for a hold that failed; a scan summary
      // passes when it flagged nothing.
      return "hold" in evidence ? false : evidence.holdsFlagged === 0
    }
    return (
      evidence.numerator.sources.every((source) => source.present) &&
      evidence.denominator.sources.every((source) => source.present) &&
      evidence.undefinedReason === undefined &&
      rationalWithinBounds(evidence.observedRatio, evidence.effectiveBounds)
    )
  } catch {
    return false
  }
}

function rationalWithinBounds(
  value: Rational | undefined,
  bounds: { min: string; max: string }
): boolean {
  if (!value) return false
  const numerator = BigInt(value.numerator)
  let denominator = BigInt(value.denominator)
  if (denominator === 0n) return false
  let normalizedNumerator = numerator
  if (denominator < 0n) {
    normalizedNumerator = -normalizedNumerator
    denominator = -denominator
  }
  const min = decimalFraction(bounds.min)
  const max = decimalFraction(bounds.max)
  return (
    normalizedNumerator * min.denominator >= min.numerator * denominator &&
    normalizedNumerator * max.denominator <= max.numerator * denominator
  )
}

function decimalFraction(value: string): RationalV2AsBigInt {
  if (!/^\d+(?:\.\d+)?$/.test(value)) throw new Error("invalid decimal")
  const [whole, fraction = ""] = value.split(".")
  const denominator = 10n ** BigInt(fraction.length)
  return {
    numerator: BigInt(`${whole}${fraction}`),
    denominator,
  }
}

interface RationalV2AsBigInt {
  numerator: bigint
  denominator: bigint
}

function displaySourceId(sources: EvidenceSource[], id: string): string {
  const source = sources.find((candidate) => candidate.id === id)
  return source ? sourceName(source) : id
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

function strings(value: Record<string, unknown>, keys: string[]): boolean {
  return keys.every((key) => typeof value[key] === "string")
}

function isEvidenceSource(value: unknown): value is EvidenceSource {
  return (
    isRecord(value) &&
    strings(value, ["id", "kind", "asset", "balance"]) &&
    (value.label === undefined || typeof value.label === "string") &&
    (value.kind === "ledger" || value.kind === "account_metadata") &&
    typeof value.present === "boolean"
  )
}

function isEquationSource(
  value: unknown
): value is BalanceEquationEvidenceSource {
  return (
    isEvidenceSource(value) &&
    "coefficient" in value &&
    typeof value.coefficient === "number" &&
    Number.isSafeInteger(value.coefficient) &&
    "contribution" in value &&
    typeof value.contribution === "string"
  )
}

function isRational(value: unknown): value is Rational {
  return isRecord(value) && strings(value, ["numerator", "denominator"])
}

function optionalRational(value: unknown): boolean {
  return value === undefined || isRational(value)
}

function isBounds(value: unknown): boolean {
  return isRecord(value) && strings(value, ["min", "max"])
}

function isPortfolio(value: unknown): boolean {
  return (
    isRecord(value) &&
    Array.isArray(value.sources) &&
    value.sources.every(isEquationSource) &&
    typeof value.total === "string"
  )
}

export function StructuredEvidenceFallback({
  evidence,
}: {
  evidence: unknown
}) {
  const rows = flattenStructured(evidence)
  if (rows.length === 0)
    return (
      <p className="text-sm text-muted-foreground">
        No usable structured evidence was recorded.
      </p>
    )
  return (
    <div className="rounded-md border border-dashed p-3">
      <div className="mb-2 text-xs font-medium text-muted-foreground">
        Structured V2 evidence from an unknown or malformed operation
      </div>
      <dl className="grid gap-x-4 gap-y-1 sm:grid-cols-[minmax(8rem,auto)_minmax(0,1fr)]">
        {rows.map(([key, value]) => (
          <div key={key} className="contents">
            <dt className="font-mono text-xs break-all text-muted-foreground">
              {key}
            </dt>
            <dd className="font-mono text-xs break-all">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function flattenStructured(
  value: unknown,
  prefix = "",
  depth = 0
): Array<[string, string]> {
  if (depth > 5) return [[prefix || "value", "[nested value]"]]
  if (value === null || value === undefined)
    return prefix ? [[prefix, String(value)]] : []
  if (typeof value !== "object") return [[prefix || "value", String(value)]]
  if (Array.isArray(value))
    return value.flatMap((entry, index) =>
      flattenStructured(entry, `${prefix}[${index}]`, depth + 1)
    )
  return Object.entries(value as Record<string, unknown>).flatMap(
    ([key, entry]) =>
      flattenStructured(entry, prefix ? `${prefix}.${key}` : key, depth + 1)
  )
}
