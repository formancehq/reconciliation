/**
 * Client-side analytics for the recon UI. There is NO aggregation / time-series
 * endpoint in the API — every series here is derived from the raw resources the
 * backend does expose: `GET /rules`, `GET /rules/{id}/captures`, `GET /alerts`.
 *
 * Kept framework-free (no JSX, no React) so it stays unit-testable in isolation
 * (see analytics.test.ts) and any view can import it.
 *
 * ── Data facts these helpers are built around (verified against the live API) ──
 *   • Amounts (`signedDiff`, `difference`, `balance`, `*Balance`) arrive as
 *     STRINGS in the asset's minor units; bounds (`tolerance`, `min`, `max`)
 *     arrive as JSON numbers. `parseAmount` accepts both.
 *   • A capture's `evidence` is an ARRAY of per-fingerprint results
 *     `[{ fingerprint, passed, evidence:{…} }]` on a FAIL, and is EMPTY/absent on
 *     a PASS — a pass records no magnitude. So the deviation series has numeric
 *     points on fails only; passes are tracked as run occurrences, not values.
 *   • The authoritative reference band (tolerance / bounds) lives on the rule's
 *     `templateSpec`, not the capture — so we can draw the safe zone even for a
 *     rule that has never failed.
 *   • An asset code may carry a precision suffix (`EUR/2`); the digits after `/`
 *     are the number of minor-unit decimals. A bare code (`EUR`) implies scale 0.
 */
import type { AlertStatus, Verdict } from "./types"
import type { AnyAlert, AnyCapture, AnyRule } from "./resources"
import { contractVersionOf } from "./resources"
import { templateLabel } from "./v2"

// ── Amount / asset primitives ───────────────────────────────────────────────

/** Parse a minor-unit amount sent as a string (balances/diffs) or a JSON number
 *  (tolerance/min/max). Returns `undefined` for anything non-numeric. */
export function parseAmount(v: unknown): number | undefined {
  if (typeof v === "number") return Number.isFinite(v) ? v : undefined
  if (typeof v === "string") {
    const t = v.trim()
    if (t === "") return undefined
    const n = Number(t)
    if (!Number.isFinite(n)) return undefined
    if (/^-?\d+$/.test(t) && !Number.isSafeInteger(n)) return undefined
    return n
  }
  return undefined
}

/** Minor-unit precision from a Formance asset code: `EUR/2` → 2, `USD` → 0. */
export function assetScale(asset: string | undefined | null): number {
  if (!asset) return 0
  const i = asset.lastIndexOf("/")
  if (i < 0) return 0
  const n = Number(asset.slice(i + 1))
  return Number.isInteger(n) && n >= 0 ? n : 0
}

/** Currency part before the scale suffix: `EUR/2` → `EUR`, `USD` → `USD`. */
export function assetCode(asset: string | undefined | null): string {
  if (!asset) return ""
  const i = asset.lastIndexOf("/")
  return i < 0 ? asset : asset.slice(0, i)
}

/** Format a minor-unit integer for display, honouring the asset's precision. */
export function formatAmount(
  minor: number | undefined,
  asset: string | undefined
): string {
  if (minor === undefined || Number.isNaN(minor)) return "—"
  const scale = assetScale(asset)
  const value = minor / 10 ** scale
  return value.toLocaleString(undefined, {
    minimumFractionDigits: scale,
    maximumFractionDigits: scale,
  })
}

// ── Deviation model (one rule's captures over time) ──────────────────────────

/** Which template kinds the deviation chart understands today. */
export const DEVIATION_KINDS = ["balance_bounds", "balance_equation"] as const

export interface DeviationBounds {
  /** parity: symmetric ± band around zero (minor units). */
  tolerance?: number
  /** threshold: lower / upper bound (minor units). */
  min?: number
  max?: number
}

/** One failing capture, as a plottable point. */
export interface DeviationPoint {
  at: string // capturedAt ISO
  t: number // epoch ms (x)
  /** parity → signedDiff, threshold → balance (minor units). */
  value: number
  /** Exact wire value retained for V2 tooltips; the number is chart-only. */
  exactValue?: string
  passed?: boolean
  // parity-only context (undefined for threshold)
  difference?: number
  leftBalance?: number
  rightBalance?: number
  leftSource?: string
  rightSource?: string
}

/** All failing points for one asset/fingerprint within a rule. */
export interface DeviationSeries {
  /** Stable id (the capture fingerprint, e.g. `asset:EUR/2`) — colour anchor. */
  key: string
  /** Asset as it appears in evidence/spec (may include a `/N` suffix). */
  asset: string
  scale: number
  bounds: DeviationBounds
  points: DeviationPoint[] // chronological
}

export interface DeviationModel {
  kind: AnyRule["templateKind"]
  /** parity/threshold → true; other kinds carry no plottable deviation today. */
  supported: boolean
  /** parity → "signedDiff", threshold → "balance". */
  metric: "balance" | "residual" | "none"
  /** One entry per asset (from fail evidence, or spec fallback when never failed). */
  series: DeviationSeries[]
  /** Runs with a `pass` verdict (no magnitude recorded — occurrence only). */
  passRuns: Array<{ at: string; t: number }>
  counts: { total: number; pass: number; fail: number }
  /** passes / (pass + fail); undefined when there are no runs. */
  passRate: number | undefined
  span?: { from: string; to: string }
}

function epoch(ts: string): number {
  const t = new Date(ts).getTime()
  return Number.isNaN(t) ? 0 : t
}

function isRealTime(ts: string | undefined | null): ts is string {
  return (
    !!ts &&
    !ts.startsWith("0001-01-01") &&
    !ts.startsWith("0000") &&
    !Number.isNaN(new Date(ts).getTime())
  )
}

/** Reference bounds declared on the rule, keyed by asset CODE (scale-stripped). */
function specBounds(
  rule: Pick<AnyRule, "templateKind" | "templateSpec">
): Record<string, DeviationBounds> {
  const spec = (rule.templateSpec ?? {}) as unknown as Record<string, unknown>
  const out: Record<string, DeviationBounds> = {}
  if (rule.templateKind === "balance_bounds") {
    // V2 encodes amounts as strings; parseAmount already takes both shapes.
    const bounds = spec.bounds as
      | Record<string, { min?: unknown; max?: unknown }>
      | undefined
    if (bounds)
      for (const [asset, b] of Object.entries(bounds))
        out[asset] = { min: parseAmount(b?.min), max: parseAmount(b?.max) }
  } else if (rule.templateKind === "balance_equation") {
    // A symmetric residual band, like V1 parity's tolerance. One declared asset
    // per source, so the band applies to the assets the sources name.
    const tolerance = parseAmount(spec.tolerance)
    const sources = (spec.sources ?? []) as Array<{ asset?: unknown }>
    if (tolerance !== undefined)
      for (const source of sources)
        if (typeof source?.asset === "string" && source.asset !== "*")
          out[source.asset] = { tolerance }
  }
  return out
}

/** Normalize a capture's evidence into per-fingerprint records (empty on pass). */
function evidenceRecords(
  evidence: unknown
): Array<{ fingerprint?: string; e: Record<string, unknown> }> {
  if (!Array.isArray(evidence)) return []
  return (evidence as Array<Record<string, unknown>>).map((g) => ({
    fingerprint: typeof g.fingerprint === "string" ? g.fingerprint : undefined,
    e: {
      ...((g.evidence && typeof g.evidence === "object"
        ? g.evidence
        : g) as Record<string, unknown>),
      ...(typeof g.passed === "boolean" ? { __passed: g.passed } : {}),
    },
  }))
}

/**
 * Build the deviation-over-time model for one rule from its captures.
 * The reference band always comes from the rule's `templateSpec`; captures
 * supply the failing magnitudes.
 */
export function buildDeviationModel(
  rule: AnyRule,
  captures: AnyCapture[]
): DeviationModel {
  const kind = rule.templateKind
  const supported = (DEVIATION_KINDS as readonly string[]).includes(kind)
  const metric: DeviationModel["metric"] =
    kind === "balance_bounds"
      ? "balance"
      : kind === "balance_equation"
        ? "residual"
        : "none"

  // Reference bands used to be read from V1 specs only, which left every V2
  // rule's deviation chart without the band its V1 equivalent had. The V2
  // templates that declare a band now feed the same model.
  const bounds = specBounds(rule)
  const timed = captures.filter((c) => isRealTime(c.capturedAt))
  const verdicts = timed.filter(
    (capture) => capture.verdict === "pass" || capture.verdict === "fail"
  )
  const total = verdicts.length
  const pass = verdicts.filter(
    (c) => c.verdict === ("pass" satisfies Verdict)
  ).length
  const fail = total - pass

  const passRuns = timed
    .filter(
      (c) => c.verdict === "pass" && evidenceRecords(c.evidence).length === 0
    )
    .map((c) => ({ at: c.capturedAt, t: epoch(c.capturedAt) }))
    .sort((a, b) => a.t - b.t)

  // Collect failing points, grouped by fingerprint.
  const byKey = new Map<string, DeviationSeries>()
  if (supported) {
    for (const c of timed) {
      for (const { fingerprint, e } of evidenceRecords(c.evidence)) {
        const asset = String(
          e.asset ?? assetCode(fingerprint?.replace(/^asset:/, ""))
        )
        // balance_bounds nests the observed balance under `source`; a capture
        // recorded before retirement carried it top-level. Accept both.
        const source = (e.source ?? {}) as Record<string, unknown>
        const raw =
          metric === "balance"
            ? (e.balance ?? source.balance)
            : metric === "residual"
              ? e.residual
              : undefined
        const value = parseAmount(raw)
        if (value === undefined) continue
        const key = fingerprint ?? `asset:${asset}`
        let s = byKey.get(key)
        if (!s) {
          s = {
            key,
            asset,
            scale: assetScale(asset),
            // Prefer the authoritative spec band; fall back to per-capture evidence.
            bounds: bounds[assetCode(asset)] ?? {
              tolerance: parseAmount(e.tolerance),
              min: parseAmount(e.min),
              max: parseAmount(e.max),
            },
            points: [],
          }
          byKey.set(key, s)
        }
        const point: DeviationPoint = {
          at: c.capturedAt,
          t: epoch(c.capturedAt),
          value,
          exactValue: typeof raw === "string" ? raw : undefined,
          passed: typeof e.__passed === "boolean" ? e.__passed : undefined,
        }
        s.points.push(point)
      }
    }
  }

  // Never-failed (but supported) rule: synthesize one band-only series per spec asset
  // so the safe zone still renders.
  let series = Array.from(byKey.values())
  if (supported && series.length === 0) {
    series = Object.entries(bounds).map(([asset, b]) => ({
      key: `asset:${asset}`,
      asset,
      scale: assetScale(asset),
      bounds: b,
      points: [],
    }))
  }
  for (const s of series) s.points.sort((a, b) => a.t - b.t)

  const allTimes = timed
    .map((c) => epoch(c.capturedAt))
    .filter((t) => t > 0)
    .sort((a, b) => a - b)
  const span =
    allTimes.length > 0
      ? {
          from: new Date(allTimes[0]!).toISOString(),
          to: new Date(allTimes[allTimes.length - 1]!).toISOString(),
        }
      : undefined

  return {
    kind,
    supported,
    metric,
    series,
    passRuns,
    counts: { total, pass, fail },
    passRate: total > 0 ? pass / total : undefined,
    span,
  }
}

// ── Cumulative drift (running sum of the signed gap) ─────────────────────────

export interface DriftPoint {
  at: string
  t: number
  verdict: Verdict
  /** This run's signed contribution (parity: signedDiff; threshold: signed breach). */
  contribution: number
  /** Running total up to and including this run. */
  cumulative: number
}

/**
 * Running sum of the signed gap over time for one asset series — surfaces a
 * systematic bias (e.g. a source that is persistently over) that a per-run view
 * hides. Pass runs contribute 0 (within tolerance → no recorded magnitude), so
 * the line is flat across passes and steps on breaks.
 *
 *   • parity    → contribution = signedDiff.
 *   • threshold → contribution = signed distance outside the band
 *                 (balance − min when below, balance − max when above, else 0).
 */
export function buildDriftSeries(
  model: Pick<DeviationModel, "metric" | "passRuns">,
  series: DeviationSeries
): DriftPoint[] {
  const contributionOf = (value: number): number => {
    // A residual is already the signed distance from the target, so it counts in
    // full; a bounded balance contributes only what falls outside its limits.
    if (model.metric === "residual") return value
    const { min, max } = series.bounds
    if (min !== undefined && value < min) return value - min
    if (max !== undefined && value > max) return value - max
    return 0
  }

  const events: Array<Omit<DriftPoint, "cumulative">> = [
    ...series.points.map((p) => ({
      at: p.at,
      t: p.t,
      verdict: "fail" as Verdict,
      contribution: contributionOf(p.value),
    })),
    ...model.passRuns.map((r) => ({
      at: r.at,
      t: r.t,
      verdict: "pass" as Verdict,
      contribution: 0,
    })),
  ].sort((a, b) => a.t - b.t)

  let cumulative = 0
  return events.map((e) => {
    cumulative += e.contribution
    return { ...e, cumulative }
  })
}

// ── Break diagnosis: distribution by violated rule type ──────────────────────

export interface BreaksByKind {
  kind: AnyRule["templateKind"]
  label: string
  total: number
  OPEN: number
  ACKNOWLEDGED: number
  RESOLVED: number
}

/**
 * Alerts (breaks) grouped by the template kind of the rule that raised them —
 * "which kind of check is breaking the most", split by current status.
 * Sorted by total, descending.
 */
export function breaksByTemplateKind(
  alerts: AnyAlert[],
  rules: AnyRule[]
): BreaksByKind[] {
  const kindOf = new Map(
    rules.map((rule) => [
      `${contractVersionOf(rule)}:${rule.id}`,
      rule.templateKind,
    ])
  )
  const acc = new Map<AnyRule["templateKind"], BreaksByKind>()
  for (const a of alerts) {
    const kind = kindOf.get(`${contractVersionOf(a)}:${a.ruleID}`)
    if (!kind) continue // orphan alert (rule deleted) — skip
    let row = acc.get(kind)
    if (!row) {
      row = {
        kind,
        label: templateLabel(kind),
        total: 0,
        OPEN: 0,
        ACKNOWLEDGED: 0,
        RESOLVED: 0,
      }
      acc.set(kind, row)
    }
    row.total += 1
    row[a.status as AlertStatus] += 1
  }
  return Array.from(acc.values()).sort((a, b) => b.total - a.total)
}
