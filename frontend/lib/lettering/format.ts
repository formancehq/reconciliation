/**
 * Presentation helpers for lettering results: exact amounts in their asset, and
 * enum → badge metadata (the verdict chips follow the Alerts tab's palette:
 * green for a pass, red for breaks, amber for no conclusion).
 */
import type { EnumMeta } from "@/lib/recon/format"
import type { LetteringVerdict } from "./manifest"

const GROUP = new Intl.NumberFormat("en-US")

/** Decimal places from the asset's scale suffix: `EUR/2` → 2, `USD` or a bad suffix → 0. */
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

/**
 * Integer minor units in an asset (`EUR/2`: 2 decimals) as major units with
 * grouping: `-15000` in `EUR/2` is `−150.00`. Exact: a bigint never goes through
 * a float.
 */
export function formatMinor(
  minor: bigint | string | number | null | undefined,
  asset: string,
  { signed = false }: { signed?: boolean } = {}
): string {
  if (minor === null || minor === undefined || minor === "") return "—"
  const value = BigInt(minor)
  const scale = assetScale(asset)
  const negative = value < 0n
  const abs = negative ? -value : value
  const unit = 10n ** BigInt(scale)
  const whole = GROUP.format(abs / unit)
  const frac = scale > 0 ? "." + (abs % unit).toString().padStart(scale, "0") : ""
  const sign = negative ? "−" : signed && value > 0n ? "+" : ""
  return `${sign}${whole}${frac}`
}

export const LETTERING_VERDICT_META: Record<LetteringVerdict, EnumMeta> = {
  reconciled: { label: "Reconciled", variant: "valid" },
  reconciled_with_pending: { label: "Reconciled · pending", variant: "sky" },
  reconciled_with_warnings: { label: "Reconciled · warnings", variant: "warning" },
  breaks: { label: "Breaks", variant: "destructive" },
  incomplete: { label: "Incomplete", variant: "amber" },
}

export const PRIORITY_META: Record<number, EnumMeta> = {
  1: { label: "P1", variant: "red" },
  2: { label: "P2", variant: "orange" },
  3: { label: "P3", variant: "amber" },
  4: { label: "P4", variant: "zinc" },
}

export const LIFECYCLE_META: Record<string, EnumMeta> = {
  new: { label: "New", variant: "blue" },
  persisting: { label: "Persisting", variant: "secondary" },
  resolved: { label: "Resolved", variant: "valid" },
  cleared: { label: "Cleared", variant: "valid" },
}

export const OUTCOME_META: Record<string, EnumMeta> = {
  ok: { label: "OK", variant: "zinc" },
  pending: { label: "Pending", variant: "sky" },
  break: { label: "Break", variant: "red" },
}

/** `under_applied` → `under applied`: classes and reasons as words. */
export function words(value: string): string {
  return value.replace(/_/g, " ")
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

/** `2026-09-24` → `24 Sep 2026`, the results doc's way. A day is a calendar day: no timezone applies. */
export function formatDay(day: string | null | undefined): string {
  const m = day?.match(/^(\d{4})-(\d{2})-(\d{2})$/)
  if (!m) return day || "—"
  return `${Number(m[3])} ${MONTHS[Number(m[2]) - 1]} ${m[1]}`
}
