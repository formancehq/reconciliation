"use client"

/**
 * Insights — the analytics surface (4th recon tab). Charts are derived entirely
 * client-side from the raw API (no aggregation endpoint exists); see
 * lib/recon/analytics.ts.
 *
 * Kept intentionally lean — two essential charts, one per question:
 *   1. "Deviation over time" (per-rule) — parity signedDiff vs ± tolerance band,
 *      threshold balance vs [min, max]. Magnitudes exist on FAIL captures only
 *      (a pass records no value), so the line plots breaks; passes are shown as
 *      low-emphasis markers inside the safe zone.
 *   2. "Breaks by rule type" (global) — alerts grouped by the violated template.
 *
 * A cumulative-drift chart (running sum of the signed gap) lived here too; it was
 * removed to keep the first version simple — it is a power-user refinement of the
 * deviation trend. buildDriftSeries in lib/recon/analytics.ts is retained for when
 * it returns.
 */
import { useMemo, useState } from "react"
import {
  ComposedChart,
  Line,
  Scatter,
  Bar,
  BarChart,
  ReferenceArea,
  ReferenceLine,
  XAxis,
  YAxis,
  CartesianGrid,
} from "recharts"
import {
  LineChart as LineChartIcon,
  PieChart as PieChartIcon,
} from "lucide-react"
import { Card } from "@/components/ui/card"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  ChartContainer,
  ChartTooltip,
  type TChartConfig,
} from "@/components/ui/chart"
import {
  useReconResource,
  listAllRules,
  listAllAlerts,
  listCapturesByContract,
  contractVersionOf,
  buildDeviationModel,
  breaksByTemplateKind,
  formatAmount,
  assetCode,
  assetScale,
  STATUS_META,
  describeAnyRule,
  templateLabel,
  type AnyRule,
  type AnyAlert,
  type AnyCapture,
  type AlertStatus,
  type DeviationModel,
  type DeviationSeries,
  type DeviationPoint,
  type BreaksByKind,
} from "@/lib/recon"
import { useReconNav } from "../ReconContext"
import { Loading, ErrorState, EmptyState } from "../ui"

const CHART_CONFIG: TChartConfig = {
  value: { label: "Deviation", color: "var(--chart-4)" },
  passY: { label: "Pass", color: "var(--color-green-foreground)" },
}

interface TopData {
  rules: AnyRule[]
  alerts: AnyAlert[]
}

export function InsightsPanel() {
  const { dataVersion } = useReconNav()
  const topRes = useReconResource<TopData>(async (signal) => {
    const [rules, alerts] = await Promise.all([
      listAllRules(signal),
      listAllAlerts(signal),
    ])
    return { rules, alerts }
  }, [dataVersion])
  const [ruleKey, setRuleKey] = useState<string>()

  const rules = useMemo(() => topRes.data?.rules ?? [], [topRes.data])
  const alerts = useMemo(() => topRes.data?.alerts ?? [], [topRes.data])

  // Default to the first parity/threshold rule (the kinds the chart plots), else
  // the first rule. Derived at render (no effect): the user's pick wins while it
  // still exists, otherwise we fall back to the default.
  const defaultRuleKey = useMemo(() => {
    if (rules.length === 0) return undefined
    const preferred = rules.find(
      (r) =>
        r.templateKind === "source_parity" ||
        r.templateKind === "account_threshold"
    )
    const rule = preferred ?? rules[0]!
    return `${contractVersionOf(rule)}:${rule.id}`
  }, [rules])
  const selectedKey =
    ruleKey &&
    rules.some((rule) => `${contractVersionOf(rule)}:${rule.id}` === ruleKey)
      ? ruleKey
      : defaultRuleKey
  const rule = rules.find(
    (candidate) =>
      `${contractVersionOf(candidate)}:${candidate.id}` === selectedKey
  )

  const capturesRes = useReconResource<AnyCapture[]>(
    (signal) =>
      rule
        ? listCapturesByContract(rule.id, contractVersionOf(rule), { signal })
        : Promise.resolve([]),
    [selectedKey, dataVersion]
  )

  if (topRes.loading) return <Loading label="Loading insights…" />
  if (topRes.error)
    return <ErrorState error={topRes.error} onRetry={topRes.refetch} />
  if (rules.length === 0) {
    return (
      <div className="p-6">
        <EmptyState
          icon={<LineChartIcon className="h-7 w-7" />}
          title="No rules to analyse yet"
        >
          Create a rule and evaluate it a few times to build up history worth
          charting.
        </EmptyState>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-3 sm:p-4">
      <section className="space-y-4">
        <div className="flex flex-wrap items-center gap-3">
          <div>
            <h2 className="text-base font-semibold">Deviation over time</h2>
            <p className="text-xs text-muted-foreground">
              How far each reconciliation run sits from its tolerance, per rule.
            </p>
          </div>
          <div className="w-full sm:ml-auto sm:w-auto">
            <Select value={selectedKey} onValueChange={setRuleKey}>
              <SelectTrigger
                className="h-8 w-full text-xs sm:w-[18rem]"
                aria-label="Choose a rule"
              >
                <SelectValue placeholder="Choose a rule" />
              </SelectTrigger>
              <SelectContent>
                {rules.map((r) => (
                  <SelectItem
                    key={`${contractVersionOf(r)}:${r.id}`}
                    value={`${contractVersionOf(r)}:${r.id}`}
                    className="text-xs"
                  >
                    {r.name}
                    <span className="ml-1.5 text-muted-foreground">
                      · {templateLabel(r.templateKind)}
                      {contractVersionOf(r) === 2 ? " · V2" : ""}
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        {rule && (
          <DeviationCard
            key={rule.id}
            rule={rule}
            captures={capturesRes.data ?? []}
            loading={capturesRes.loading}
            error={capturesRes.error}
            onRetry={capturesRes.refetch}
          />
        )}
      </section>

      <BreaksByTypeCard alerts={alerts} rules={rules} />
    </div>
  )
}

function DeviationCard({
  rule,
  captures,
  loading,
  error,
  onRetry,
}: {
  rule: AnyRule
  captures: AnyCapture[]
  loading: boolean
  error: unknown
  onRetry: () => void
}) {
  const model = useMemo(
    () => buildDeviationModel(rule, captures),
    [rule, captures]
  )
  const [seriesKey, setSeriesKey] = useState<string>()

  // Active asset series, derived at render: the user's pick wins while valid.
  const activeKey =
    seriesKey && model.series.some((s) => s.key === seriesKey)
      ? seriesKey
      : model.series[0]?.key
  const series = model.series.find((s) => s.key === activeKey)

  return (
    <Card className="min-w-0 overflow-hidden p-3 sm:p-4">
      {/* Header: template summary + verdict tallies */}
      <div className="mb-3 flex flex-wrap items-baseline gap-x-4 gap-y-1">
        <span className="min-w-0 font-mono text-xs break-all text-foreground/80">
          {describeAnyRule(rule)}
        </span>
        <span className="w-full text-xs text-muted-foreground sm:ml-auto sm:w-auto">
          {model.counts.total} run{model.counts.total === 1 ? "" : "s"} ·{" "}
          <span className="text-green-foreground">
            {model.counts.pass} pass
          </span>{" "}
          · <span className="text-destructive">{model.counts.fail} fail</span>
          {model.passRate !== undefined && (
            <> · {Math.round(model.passRate * 100)}% pass rate</>
          )}
        </span>
      </div>

      {/* Asset picker when the rule spans more than one asset/fingerprint */}
      {model.series.length > 1 && (
        <div className="mb-3 flex items-center gap-2">
          <span className="text-xs text-muted-foreground">Asset</span>
          <Select value={series?.key} onValueChange={setSeriesKey}>
            <SelectTrigger
              className="h-7 w-40 text-xs"
              aria-label="Choose an asset"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {model.series.map((s) => (
                <SelectItem key={s.key} value={s.key} className="text-xs">
                  {assetCode(s.asset)}
                  {assetScale(s.asset) > 0 ? ` /${assetScale(s.asset)}` : ""}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      {loading && captures.length === 0 ? (
        <Loading label="Loading history…" />
      ) : error ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : !model.supported ? (
        <EmptyState
          icon={<LineChartIcon className="h-7 w-7" />}
          title="No deviation to plot for this rule"
        >
          No semantically valid series is defined for this operation. This is a{" "}
          {templateLabel(rule.templateKind)} rule.
        </EmptyState>
      ) : model.counts.total === 0 ? (
        <EmptyState
          icon={<LineChartIcon className="h-7 w-7" />}
          title="No captures yet"
        >
          Evaluate this rule from its detail view to record runs, then come
          back.
        </EmptyState>
      ) : series ? (
        <>
          <DeviationChart model={model} series={series} />
          <ChartCaption model={model} series={series} />
        </>
      ) : null}
    </Card>
  )
}

/** Axis tick label, adapted to the visible span. Captures are wall-clock-stamped
 *  at evaluation time, so a burst of runs can span seconds — show seconds when
 *  tight, minutes/date as the window widens. */
function fmtAxis(t: number, spanMs: number): string {
  const d = new Date(t)
  if (spanMs < 6 * 3_600_000) {
    return d.toLocaleTimeString(
      undefined,
      spanMs < 15 * 60_000
        ? { hour: "2-digit", minute: "2-digit", second: "2-digit" }
        : { hour: "2-digit", minute: "2-digit" }
    )
  }
  if (spanMs < 3 * 86_400_000) {
    return d.toLocaleString(undefined, {
      month: "short",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    })
  }
  return d.toLocaleDateString(undefined, { month: "short", day: "2-digit" })
}

/** Full, unambiguous timestamp for tooltips. */
function fmtFull(t: number): string {
  return new Date(t).toLocaleString(undefined, {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

/** A chart row: a fail point (carries the DeviationPoint context) or a pass marker. */
type Row = Partial<DeviationPoint> & {
  t: number
  kind: "fail" | "pass"
  /** y position for a pass marker (pinned inside the safe zone). */
  passY?: number
}

function DeviationChart({
  model,
  series,
}: {
  model: DeviationModel
  series: DeviationSeries
}) {
  const isParity = model.metric === "signedDiff" || model.metric === "residual"
  const { tolerance, min, max } = series.bounds

  // Reference band edges.
  const bandLow = isParity ? -(tolerance ?? 0) : min
  const bandHigh = isParity ? (tolerance ?? 0) : max

  // Y domain from all values + band edges + (parity) zero, padded.
  const ys: number[] = series.points.map((p) => p.value)
  if (isParity) ys.push(0)
  for (const b of [bandLow, bandHigh, min, max])
    if (typeof b === "number" && Number.isFinite(b)) ys.push(b)
  const lo = ys.length ? Math.min(...ys) : 0
  const hi = ys.length ? Math.max(...ys) : 1
  const pad = (hi - lo || Math.abs(hi) || 1) * 0.12
  const yDomain: [number, number] = [lo - pad, hi + pad]

  // Where to pin pass markers: inside the safe zone, honestly (no fabricated magnitude).
  const passY = isParity
    ? 0
    : min !== undefined && max !== undefined
      ? (min + max) / 2
      : (min ?? max ?? (lo + hi) / 2)

  const failData: Row[] = series.points
    .filter((point) => point.passed !== true)
    .map((p) => ({ ...p, kind: "fail" }))
  const retainedPassData: Row[] = series.points
    .filter((point) => point.passed === true)
    .map((p) => ({ ...p, kind: "pass" }))
  const passData: Row[] = model.passRuns.map((r) => ({
    t: r.t,
    passY,
    kind: "pass",
  }))

  const times = [
    ...failData.map((r) => r.t),
    ...retainedPassData.map((r) => r.t),
    ...passData.map((r) => r.t),
  ]
  const xDomain: [number, number] = times.length
    ? [Math.min(...times), Math.max(...times)]
    : [0, 1]
  const spanMs = xDomain[1] - xDomain[0]

  // Safe-zone shading: parity always (band or the zero line); threshold when a bound exists.
  const showBand = isParity
    ? (tolerance ?? 0) > 0
    : bandLow !== undefined || bandHigh !== undefined

  return (
    <ChartContainer
      config={CHART_CONFIG}
      className="aspect-auto h-[340px] w-full"
    >
      <ComposedChart
        data={failData}
        margin={{ top: 8, right: 16, bottom: 4, left: 4 }}
      >
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis
          dataKey="t"
          type="number"
          scale="time"
          domain={xDomain}
          tickFormatter={(v: number) => fmtAxis(v, spanMs)}
          tickLine={false}
          axisLine={false}
          minTickGap={48}
          tickMargin={8}
        />
        <YAxis
          type="number"
          domain={yDomain}
          width={72}
          tickLine={false}
          axisLine={false}
          tickFormatter={(v: number) => formatAmount(v, series.asset)}
        />

        {showBand && (
          <ReferenceArea
            y1={bandLow ?? yDomain[0]}
            y2={bandHigh ?? yDomain[1]}
            fill="var(--color-green-background)"
            fillOpacity={0.55}
            stroke="none"
            ifOverflow="extendDomain"
          />
        )}
        {isParity && (
          <ReferenceLine
            y={0}
            stroke="var(--color-green-foreground)"
            strokeDasharray="4 4"
            strokeOpacity={0.7}
          />
        )}
        {!isParity && min !== undefined && (
          <ReferenceLine
            y={min}
            stroke="var(--color-green-foreground)"
            strokeDasharray="4 4"
            strokeOpacity={0.8}
            label={{
              value: "min",
              position: "insideBottomLeft",
              fontSize: 10,
              fill: "var(--color-green-foreground)",
            }}
          />
        )}
        {!isParity && max !== undefined && (
          <ReferenceLine
            y={max}
            stroke="var(--color-green-foreground)"
            strokeDasharray="4 4"
            strokeOpacity={0.8}
            label={{
              value: "max",
              position: "insideTopLeft",
              fontSize: 10,
              fill: "var(--color-green-foreground)",
            }}
          />
        )}

        <ChartTooltip
          cursor={{ stroke: "var(--border)" }}
          content={<DeviationTooltip model={model} series={series} />}
        />

        {/* Break magnitudes (fails). connectNulls irrelevant — data is fail-only. */}
        <Line
          type="monotone"
          dataKey="value"
          stroke="var(--chart-4)"
          strokeWidth={2}
          dot={{
            r: 3,
            fill: "var(--color-destructive)",
            stroke: "var(--color-destructive)",
          }}
          activeDot={{ r: 5 }}
          isAnimationActive={false}
        />
        {/* Pass runs — pinned inside the safe zone (no magnitude recorded). */}
        {passData.length > 0 && (
          <Scatter
            data={passData}
            dataKey="passY"
            fill="var(--color-green-foreground)"
            shape="circle"
            isAnimationActive={false}
          />
        )}
        {retainedPassData.length > 0 && (
          <Scatter
            data={retainedPassData}
            dataKey="value"
            fill="var(--color-green-foreground)"
            shape="circle"
            isAnimationActive={false}
          />
        )}
      </ComposedChart>
    </ChartContainer>
  )
}

function DeviationTooltip({
  active,
  payload,
  series,
  model,
}: {
  active?: boolean
  payload?: Array<{ payload?: Row }>
  series: DeviationSeries
  model: DeviationModel
}) {
  if (!active || !payload?.length) return null
  const row = payload[0]?.payload
  if (!row) return null

  return (
    <div className="grid min-w-[11rem] gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl">
      <div className="font-medium">{fmtFull(row.t)}</div>
      {row.value !== undefined ? (
        <>
          <Line2
            label={metricLabel(model.metric)}
            value={row.exactValue ?? formatAmount(row.value, series.asset)}
            tone={
              row.kind === "pass" ? "text-green-foreground" : "text-destructive"
            }
          />
          {row.difference !== undefined && (
            <Line2
              label="|difference|"
              value={formatAmount(row.difference, series.asset)}
            />
          )}
          {row.leftBalance !== undefined && (
            <Line2
              label="Source A balance"
              value={formatAmount(row.leftBalance, series.asset)}
            />
          )}
          {row.rightBalance !== undefined && (
            <Line2
              label="Source B balance"
              value={formatAmount(row.rightBalance, series.asset)}
            />
          )}
          {series.bounds.tolerance !== undefined && (
            <Line2
              label="tolerance"
              value={`±${formatAmount(series.bounds.tolerance, series.asset)}`}
            />
          )}
          {series.bounds.min !== undefined && (
            <Line2
              label="min"
              value={formatAmount(series.bounds.min, series.asset)}
            />
          )}
          {series.bounds.max !== undefined && (
            <Line2
              label="max"
              value={formatAmount(series.bounds.max, series.asset)}
            />
          )}
        </>
      ) : (
        <div className="text-green-foreground">
          Pass — within bounds
          <div className="text-[10px] text-muted-foreground">
            (exact value not retained)
          </div>
        </div>
      )}
    </div>
  )
}

function Line2({
  label,
  value,
  tone,
}: {
  label: string
  value: string
  tone?: string
}) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-muted-foreground">{label}</span>
      <span className={`font-mono tabular-nums ${tone ?? "text-foreground"}`}>
        {value}
      </span>
    </div>
  )
}

function ChartCaption({
  model,
  series,
}: {
  model: DeviationModel
  series: DeviationSeries
}) {
  return (
    <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
      <Legend swatch="var(--chart-4)" shape="line">
        {metricLabel(model.metric)}
        {series.asset ? ` (${assetCode(series.asset)})` : ""}
      </Legend>
      <Legend swatch="var(--color-destructive)" shape="dot">
        Break (fail)
      </Legend>
      {model.passRuns.length > 0 && (
        <Legend swatch="var(--color-green-foreground)" shape="dot">
          Pass (within tolerance)
        </Legend>
      )}
      <Legend swatch="var(--color-green-foreground)" shape="band">
        Safe zone
      </Legend>
    </div>
  )
}

function metricLabel(metric: DeviationModel["metric"]): string {
  if (metric === "signedDiff") return "Signed difference"
  if (metric === "balance") return "Balance"
  if (metric === "residual") return "Residual"
  return "Value"
}

function Legend({
  swatch,
  shape,
  children,
}: {
  swatch: string
  shape: "line" | "dot" | "band"
  children: React.ReactNode
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      {shape === "line" && (
        <span
          className="h-0.5 w-4 rounded-full"
          style={{ backgroundColor: swatch }}
        />
      )}
      {shape === "dot" && (
        <span
          className="h-2 w-2 rounded-full"
          style={{ backgroundColor: swatch }}
        />
      )}
      {shape === "band" && (
        <span
          className="h-2.5 w-4 rounded-sm"
          style={{
            backgroundColor: "var(--color-green-background)",
            border: `1px solid ${swatch}`,
          }}
        />
      )}
      {children}
    </span>
  )
}

// ── #3 Breaks by rule type (global) ──────────────────────────────────────────

const STATUS_BAR: { key: AlertStatus; label: string; color: string }[] = [
  {
    key: "OPEN",
    label: STATUS_META.OPEN.label,
    color: "var(--color-warning-foreground)",
  },
  {
    key: "ACKNOWLEDGED",
    label: STATUS_META.ACKNOWLEDGED.label,
    color: "var(--color-info-foreground)",
  },
  {
    key: "RESOLVED",
    label: STATUS_META.RESOLVED.label,
    color: "var(--color-valid-foreground)",
  },
]

const BREAKS_CONFIG: TChartConfig = Object.fromEntries(
  STATUS_BAR.map((s) => [s.key, { label: s.label, color: s.color }])
) as TChartConfig

function BreaksByTypeCard({
  alerts,
  rules,
}: {
  alerts: AnyAlert[]
  rules: AnyRule[]
}) {
  const rows = useMemo(
    () => breaksByTemplateKind(alerts, rules),
    [alerts, rules]
  )
  const total = rows.reduce((s, r) => s + r.total, 0)

  return (
    <section>
      <div className="mb-1 flex items-center gap-2">
        <PieChartIcon className="h-4 w-4 text-muted-foreground" />
        <h3 className="text-base font-semibold">Breaks by rule type</h3>
      </div>
      <p className="mb-3 text-xs text-muted-foreground">
        Which kind of check is breaking the most — {total} break
        {total === 1 ? "" : "s"} attributed to a live rule, by current status.
      </p>
      <Card className="p-4">
        {rows.length === 0 ? (
          <EmptyState
            icon={<PieChartIcon className="h-7 w-7" />}
            title="No breaks yet"
          >
            Alerts opened by your rules will be grouped here by template kind.
          </EmptyState>
        ) : (
          <>
            <ChartContainer
              config={BREAKS_CONFIG}
              className="aspect-auto w-full"
              style={{ height: rows.length * 52 + 28 }}
            >
              <BarChart
                data={rows}
                layout="vertical"
                margin={{ top: 4, right: 28, bottom: 4, left: 8 }}
              >
                <CartesianGrid horizontal={false} strokeDasharray="3 3" />
                <XAxis
                  type="number"
                  tickLine={false}
                  axisLine={false}
                  allowDecimals={false}
                />
                <YAxis
                  type="category"
                  dataKey="label"
                  width={124}
                  tickLine={false}
                  axisLine={false}
                />
                <ChartTooltip
                  cursor={{ fill: "var(--color-muted)", fillOpacity: 0.4 }}
                  content={<BreaksTooltip />}
                />
                {STATUS_BAR.map((s, i) => (
                  <Bar
                    key={s.key}
                    dataKey={s.key}
                    stackId="s"
                    fill={s.color}
                    stroke="var(--background)"
                    strokeWidth={2}
                    radius={i === STATUS_BAR.length - 1 ? [0, 4, 4, 0] : 0}
                    isAnimationActive={false}
                  />
                ))}
              </BarChart>
            </ChartContainer>
            <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
              {STATUS_BAR.map((s) => (
                <Legend key={s.key} swatch={s.color} shape="dot">
                  {s.label}
                </Legend>
              ))}
            </div>
          </>
        )}
      </Card>
    </section>
  )
}

function BreaksTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: Array<{ payload?: BreaksByKind }>
}) {
  if (!active || !payload?.length) return null
  const row = payload[0]?.payload
  if (!row) return null
  return (
    <div className="grid min-w-[11rem] gap-1.5 rounded-lg border border-border/50 bg-background px-2.5 py-1.5 text-xs shadow-xl">
      <div className="font-medium">
        {row.label} · {row.total} break{row.total === 1 ? "" : "s"}
      </div>
      {STATUS_BAR.map((s) =>
        row[s.key] > 0 ? (
          <Line2 key={s.key} label={s.label} value={String(row[s.key])} />
        ) : null
      )}
    </div>
  )
}
