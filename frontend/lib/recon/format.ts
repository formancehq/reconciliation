/**
 * Presentation helpers for the recon UI: enum → badge/label metadata, safe
 * timestamp formatting (recon emits `0001-01-01T00:00:00Z` zero-times for some
 * derived records), a human summary of a rule's `templateSpec`, and evidence
 * flattening. Kept framework-free (no JSX) so any view can import it.
 */
import type {
  AlertStatus,
  Cadence,
  EvaluationResult,
  Rule,
  Severity,
  TemplateKind,
  Verdict,
} from "./types"

/** Any valid `@workspace/ui` Badge variant we use. */
export type BadgeVariant =
  | "primary"
  | "secondary"
  | "outline"
  | "valid"
  | "destructive"
  | "info"
  | "warning"
  | "amber"
  | "orange"
  | "red"
  | "sky"
  | "blue"
  | "green"
  | "zinc"

export interface EnumMeta {
  label: string
  variant: BadgeVariant
}

// ── Severity ────────────────────────────────────────────────────────────────
export const SEVERITY_ORDER: Severity[] = [
  "critical",
  "high",
  "medium",
  "low",
  "info",
]

export const SEVERITY_META: Record<Severity, EnumMeta> = {
  critical: { label: "Critical", variant: "destructive" },
  high: { label: "High", variant: "orange" },
  medium: { label: "Medium", variant: "amber" },
  low: { label: "Low", variant: "sky" },
  info: { label: "Info", variant: "info" },
}

// ── Alert status ────────────────────────────────────────────────────────────
export const STATUS_META: Record<AlertStatus, EnumMeta> = {
  OPEN: { label: "Open", variant: "warning" },
  ACKNOWLEDGED: { label: "Acknowledged", variant: "info" },
  RESOLVED: { label: "Resolved", variant: "valid" },
}

// ── Verdict / evaluation result ─────────────────────────────────────────────
export const VERDICT_META: Record<Verdict, EnumMeta> = {
  pass: { label: "Pass", variant: "valid" },
  fail: { label: "Fail", variant: "destructive" },
  error: { label: "Error", variant: "amber" },
}

export const RESULT_META: Record<EvaluationResult, EnumMeta> = {
  PASS: { label: "Pass", variant: "valid" },
  FAIL: { label: "Fail", variant: "destructive" },
  ERROR: { label: "Error", variant: "amber" },
}

// ── Template kind ───────────────────────────────────────────────────────────
export const TEMPLATE_META: Record<
  TemplateKind,
  { label: string; blurb: string }
> = {
  ledger_invariant: {
    label: "Ledger invariant",
    blurb:
      "A signed sum of account sets must net to zero (per asset), within tolerance.",
  },
  source_parity: {
    label: "Source parity",
    blurb: "Two sources must match (per asset), within tolerance.",
  },
  account_threshold: {
    label: "Account threshold",
    blurb: "A balance must stay within [min, max] bounds (per asset).",
  },
}

export const TEMPLATE_KINDS = Object.keys(TEMPLATE_META) as TemplateKind[]

// ── Cadence ─────────────────────────────────────────────────────────────────
export const CADENCE_META: Record<Cadence, { label: string }> = {
  continuous: { label: "Continuous" },
  daily: { label: "Daily" },
  weekly: { label: "Weekly" },
  monthly: { label: "Monthly" },
}

// ── Timestamps ──────────────────────────────────────────────────────────────
/** Recon emits the Go zero time for some derived records; treat as "no value". */
export function isZeroTime(ts: string | undefined | null): boolean {
  return !ts || ts.startsWith("0001-01-01") || ts.startsWith("0000")
}

export function formatDateTime(ts: string | undefined | null): string {
  if (isZeroTime(ts)) return "—"
  const d = new Date(ts as string)
  if (Number.isNaN(d.getTime())) return "—"
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

/** Compact relative time ("just now", "5m ago", "3h ago", "2d ago"). */
export function formatRelative(ts: string | undefined | null): string {
  if (isZeroTime(ts)) return "—"
  const d = new Date(ts as string)
  if (Number.isNaN(d.getTime())) return "—"
  const diffMs = Date.now() - d.getTime()
  const s = Math.round(diffMs / 1000)
  if (s < 0) return "in the future"
  if (s < 45) return "just now"
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  if (h < 24) return `${h}h ago`
  const days = Math.round(h / 24)
  if (days < 30) return `${days}d ago`
  return formatDateTime(ts)
}

// ── templateSpec → human summary ─────────────────────────────────────────────
type Query = Record<string, unknown> | undefined

/** Best-effort one-liner for a go-libs `$match` query selector. */
export function summarizeQuery(query: Query): string {
  if (!query || typeof query !== "object") return "*"
  const m = (query as Record<string, unknown>).$match as
    Record<string, unknown> | undefined
  if (m) {
    const addr = m.address
    if (typeof addr === "string") return addr
    const key = Object.keys(m)[0]
    return key ? `${key}=${String(m[key])}` : "*"
  }
  if (Array.isArray((query as Record<string, unknown>).$and))
    return "(compound $and)"
  if (Array.isArray((query as Record<string, unknown>).$or))
    return "(compound $or)"
  return "*"
}

function summarizeSource(src: unknown): string {
  if (!src || typeof src !== "object") return "?"
  const s = src as Record<string, unknown>
  if (s.kind === "payments_pool") return `pool:${String(s.poolID ?? "?")}`
  const ledger = String(s.ledger ?? "?")
  const base = `${ledger}[${summarizeQuery(s.query as Query)}]`
  if (s.kind === "account_metadata") {
    const metadataKey =
      typeof s.metadataKey === "string" && s.metadataKey ? s.metadataKey : "?"
    const asset = typeof s.asset === "string" && s.asset ? s.asset : "?"
    return `${base} synced:${metadataKey} → ${asset}`
  }
  return base
}

/** A short, human summary of a rule's templateSpec for lists and detail headers. */
export function describeSpec(
  rule: Pick<Rule, "templateKind" | "templateSpec">
): string {
  const spec = (rule.templateSpec ?? {}) as Record<string, unknown>
  switch (rule.templateKind) {
    case "ledger_invariant": {
      const terms = Array.isArray(spec.terms)
        ? (spec.terms as Array<Record<string, unknown>>)
        : []
      const body = terms
        .map((t, i) => {
          const sign = Number(t.sign) < 0 ? "−" : i === 0 ? "" : "+"
          return `${sign} ${String(t.ledger ?? "?")}[${summarizeQuery(t.query as Query)}]`
        })
        .join(" ")
        .trim()
      const assets = Object.keys(
        (spec.tolerance as Record<string, unknown>) ?? {}
      )
      return `Σ ${body || "(no terms)"} = 0${assets.length ? ` · ${assets.join(", ")}` : ""}`
    }
    case "source_parity": {
      const left = summarizeSource(spec.left)
      const right = summarizeSource(spec.right)
      const scope = spec.scope === "per_account" ? " (per account)" : ""
      return `${left} ≡ ${right}${scope}`
    }
    case "account_threshold": {
      const ledger = String(spec.ledger ?? "?")
      const q = summarizeQuery(spec.query as Query)
      const mode = spec.mode === "per_account" ? " (per account)" : ""
      const bounds =
        (spec.bounds as Record<string, { min?: number; max?: number }>) ?? {}
      const parts = Object.entries(bounds).map(([asset, b]) => {
        const lo = b?.min !== undefined ? `≥ ${b.min}` : ""
        const hi = b?.max !== undefined ? `≤ ${b.max}` : ""
        return `${asset} ${[lo, hi].filter(Boolean).join(" & ")}`.trim()
      })
      return `${ledger}[${q}]${mode} within ${parts.join(", ") || "bounds"}`
    }
    default:
      return rule.templateKind
  }
}

// ── Evidence ─────────────────────────────────────────────────────────────────
/** Evidence keys that are noise in a key/value list (shown elsewhere / verbose). */
const EVIDENCE_SKIP = new Set([
  "compiledCEL",
  "fingerprint",
  "passed",
  "evaluationId",
])

const EVIDENCE_LABELS: Record<string, string> = {
  leftSource: "Source A",
  leftBalance: "Source A balance",
  rightSource: "Source B",
  rightBalance: "Source B balance",
}

/** Convert wire-level evidence keys into the reconciliation vocabulary shown to users. */
export function evidenceLabel(key: string): string {
  const known = EVIDENCE_LABELS[key]
  if (known) return known
  const words = key
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** Flatten an evidence object into label/value rows for display (skips noise). */
export function evidenceEntries(
  evidence: unknown
): Array<{ key: string; value: string }> {
  if (!evidence || typeof evidence !== "object" || Array.isArray(evidence))
    return []
  return Object.entries(evidence as Record<string, unknown>)
    .filter(([key]) => !EVIDENCE_SKIP.has(key))
    .map(([key, value]) => ({
      key,
      value: typeof value === "object" ? JSON.stringify(value) : String(value),
    }))
}

export interface EvidenceGroup {
  fingerprint?: string
  passed?: boolean
  evidence?: unknown
  rows: Array<{ key: string; value: string }>
}

/**
 * Normalize capture/evaluation evidence into displayable groups. Captures carry
 * an array of per-fingerprint results (`[{ fingerprint, passed, evidence }]`);
 * alerts carry a single flat evidence object. Handle both.
 */
export function evidenceGroups(evidence: unknown): EvidenceGroup[] {
  if (!evidence) return []
  if (Array.isArray(evidence)) {
    return (evidence as Array<Record<string, unknown>>).map((e) => {
      const raw = e.evidence ?? e
      const v2 =
        !!raw &&
        typeof raw === "object" &&
        !Array.isArray(raw) &&
        (raw as Record<string, unknown>).schemaVersion === 2
      return {
        fingerprint:
          typeof e.fingerprint === "string" ? e.fingerprint : undefined,
        passed: typeof e.passed === "boolean" ? e.passed : undefined,
        ...(v2 ? { evidence: raw } : {}),
        rows: evidenceEntries(raw),
      }
    })
  }
  if (typeof evidence === "object") {
    const v2 =
      !Array.isArray(evidence) &&
      (evidence as Record<string, unknown>).schemaVersion === 2
    return [{ ...(v2 ? { evidence } : {}), rows: evidenceEntries(evidence) }]
  }
  return []
}
