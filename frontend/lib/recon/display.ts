/**
 * Business-facing display helpers: amounts in major units, run schedules,
 * alert periods. The API and the evidence keep exact minor-unit integers;
 * these only change how they read on screen.
 */
import type { PeriodType, Schedule } from "./common"

/**
 * Format an integer amount of minor units for an asset such as "USD/2":
 * "800000" + "USD/2" → "8,000.00 USD". Exact: string arithmetic, no float.
 * Returns the raw value when it is not an integer or the asset has no
 * precision suffix.
 */
export function formatAmount(value: string | number | bigint | undefined | null, asset?: string): string {
  if (value === undefined || value === null || value === "") return "—"
  const raw = String(value).trim()
  const [code, precisionText] = (asset ?? "").split("/")
  if (!/^-?\d+$/.test(raw)) return asset ? `${raw} ${code || asset}` : raw
  const precision = precisionText && /^\d+$/.test(precisionText) ? Number(precisionText) : 0
  const negative = raw.startsWith("-")
  const digits = (negative ? raw.slice(1) : raw).replace(/^0+(?=\d)/, "")
  const padded = digits.padStart(precision + 1, "0")
  const whole = padded.slice(0, padded.length - precision) || "0"
  const fraction = precision > 0 ? padded.slice(padded.length - precision) : ""
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",")
  const number = `${negative ? "−" : ""}${grouped}${fraction ? `.${fraction}` : ""}`
  return code ? `${number} ${code}` : number
}

const PRESET_LABELS: Record<string, string> = {
  "*/5 * * * *": "every 5 minutes",
  "*/15 * * * *": "every 15 minutes",
  "*/30 * * * *": "every 30 minutes",
  "0 * * * *": "hourly",
  "0 0 * * *": "daily at 00:00",
  "0 0 * * 1": "weekly on Monday",
  "0 0 1 * *": "monthly on the 1st",
}

/** How a rule runs, in one short phrase: "Runs every 15 minutes" or "On demand only". */
export function scheduleLabel(schedule?: Schedule | null): string {
  if (!schedule || schedule.kind !== "cron" || !schedule.expr?.trim()) return "On demand only"
  const expr = schedule.expr.trim()
  const tz = schedule.tz?.trim() || "UTC"
  const preset = PRESET_LABELS[expr]
  return preset ? `Runs ${preset} (${tz})` : `Runs on cron ${expr} (${tz})`
}

/** True when the rule never runs by itself. */
export function isOnDemand(schedule?: Schedule | null): boolean {
  return !schedule || schedule.kind !== "cron" || !schedule.expr?.trim()
}

/** The alert grouping period, said as what it does. */
export function periodLabel(period: PeriodType): string {
  switch (period) {
    case "daily":
      return "One alert per day"
    case "weekly":
      return "One alert per week"
    case "monthly":
      return "One alert per month"
    default:
      return "One ongoing alert"
  }
}

function parseField(field: string, min: number, max: number): Set<number> | null {
  const out = new Set<number>()
  for (const part of field.split(",")) {
    const [range, stepText] = part.split("/")
    const step = stepText ? Number(stepText) : 1
    if (!Number.isInteger(step) || step < 1) return null
    let lo = min
    let hi = max
    if (range !== "*") {
      const [a, b] = range.split("-")
      lo = Number(a)
      hi = b === undefined ? (stepText ? max : lo) : Number(b)
      if (!Number.isInteger(lo) || !Number.isInteger(hi)) return null
    }
    for (let v = lo; v <= hi; v += step) out.add(v)
  }
  return out
}

/**
 * The next run of a five-field cron schedule after `from`, in the schedule's
 * time zone when it is UTC (the default), otherwise in local time. Returns
 * undefined for on-demand rules or an expression it cannot read.
 */
export function nextRun(schedule?: Schedule | null, from: Date = new Date()): Date | undefined {
  if (isOnDemand(schedule)) return undefined
  const fields = schedule!.expr!.trim().split(/\s+/)
  if (fields.length !== 5) return undefined
  const [mi, ho, dm, mo, dw] = [
    parseField(fields[0], 0, 59),
    parseField(fields[1], 0, 23),
    parseField(fields[2], 1, 31),
    parseField(fields[3], 1, 12),
    parseField(fields[4], 0, 6),
  ]
  if (!mi || !ho || !dm || !mo || !dw) return undefined
  const utc = (schedule!.tz?.trim() || "UTC") === "UTC"
  const t = new Date(from.getTime())
  t.setSeconds(0, 0)
  for (let i = 0; i < 60 * 24 * 366; i++) {
    t.setTime(t.getTime() + 60_000)
    const [m, h, d, mon, wd] = utc
      ? [t.getUTCMinutes(), t.getUTCHours(), t.getUTCDate(), t.getUTCMonth() + 1, t.getUTCDay()]
      : [t.getMinutes(), t.getHours(), t.getDate(), t.getMonth() + 1, t.getDay()]
    if (mi.has(m) && ho.has(h) && dm.has(d) && mo.has(mon) && dw.has(wd)) return t
  }
  return undefined
}

/** Templates shipped in Reconciliation v3.0.0 (RFC 0022). Others come in later phases. */
export const V3_TEMPLATES = ["balance_bounds", "balance_equation", "source_consensus"] as const
