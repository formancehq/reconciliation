import type {
  BalanceEquationTermV2,
  EvidenceSourceV2,
  NamedSourceV2,
  RateBoundsV2,
  RuleV2,
  TemplateKindV2,
} from "./typesV2"
import type { Rule } from "./types"
import { describeSpec, TEMPLATE_META } from "./format"

export const SOURCE_ID_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/
export const UNSIGNED_INTEGER_PATTERN = /^(0|[1-9][0-9]{0,77})$/
export const SIGNED_SAFE_INTEGER_PATTERN = /^-?[1-9][0-9]*$/
export const DECIMAL_PATTERN = /^(0|[1-9][0-9]*)(\.[0-9]{1,18})?$/
/**
 * A Go duration as `time.ParseDuration` accepts it — "48h", "90m", "1h30m".
 * The server is authoritative; this catches the obvious typo before a round-trip.
 */
export const DURATION_PATTERN = /^(\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+$/

export const TEMPLATE_META_V2: Record<
  TemplateKindV2,
  { label: string; blurb: string }
> = {
  balance_equation: {
    label: "Balance equation",
    blurb:
      "A signed equation across 2–32 named sources must net to zero within tolerance.",
  },
  exchange_rate_bounds: {
    label: "Exchange-rate bounds",
    blurb:
      "The exact quote-major-units per base-major-unit rate must stay inside inclusive bounds.",
  },
  source_consensus: {
    label: "Source consensus",
    blurb:
      "Every named source must be present and the widest observed spread must stay within tolerance.",
  },
  balance_bounds: {
    label: "Balance bounds",
    blurb:
      "One account set's balance must stay inside inclusive per-asset limits, in minor units.",
  },
  stale_holds: {
    label: "Stale holds",
    blurb:
      "Held funds whose deadline has passed — or is about to — read from each hold's own metadata.",
  },
  coverage_ratio_bounds: {
    label: "Coverage-ratio bounds",
    blurb:
      "The exact ratio between named numerator and denominator portfolios must stay inside inclusive bounds.",
  },
}

export const TEMPLATE_KINDS_V2 = Object.keys(
  TEMPLATE_META_V2
) as TemplateKindV2[]

export function templateLabel(kind: string): string {
  if (kind in TEMPLATE_META_V2)
    return TEMPLATE_META_V2[kind as TemplateKindV2].label
  if (kind in TEMPLATE_META)
    return TEMPLATE_META[kind as keyof typeof TEMPLATE_META].label
  return kind
}

export function describeAnyRule(rule: Rule | RuleV2): string {
  return "contractVersion" in rule && rule.contractVersion === 2
    ? describeRuleV2(rule)
    : describeSpec(rule as Rule)
}

export function sourceName(
  source: Pick<NamedSourceV2 | EvidenceSourceV2, "id" | "label">
): string {
  return source.label?.trim() || source.id
}

export function effectiveSourceKind(
  source: NamedSourceV2
): "ledger" | "account_metadata" {
  return source.kind ?? "ledger"
}

export function isExplicitRate(
  rate: RateBoundsV2
): rate is { min: string; max: string } {
  return "min" in rate
}

function decimalParts(value: string): [string, string] {
  const [whole, fraction = ""] = value.split(".")
  return [whole!.replace(/^0+(?=\d)/, ""), fraction]
}

/** Compare valid plain-decimal strings without converting them to Number. */
export function compareDecimalStrings(a: string, b: string): number {
  const [aw, af] = decimalParts(a)
  const [bw, bf] = decimalParts(b)
  if (aw.length !== bw.length) return aw.length < bw.length ? -1 : 1
  if (aw !== bw) return aw < bw ? -1 : 1
  const width = Math.max(af.length, bf.length)
  const ap = af.padEnd(width, "0")
  const bp = bf.padEnd(width, "0")
  return ap === bp ? 0 : ap < bp ? -1 : 1
}

function signedTerm(name: string, coefficient: number, first: boolean): string {
  const negative = coefficient < 0
  const magnitude = Math.abs(coefficient)
  const value = `${magnitude === 1 ? "" : `${magnitude} × `}${name}`
  if (first) return negative ? `−${value}` : value
  return `${negative ? "−" : "+"} ${value}`
}

export function readableTerms(
  sources: NamedSourceV2[],
  terms: BalanceEquationTermV2[]
): string {
  const byId = new Map(sources.map((source) => [source.id, source]))
  return terms
    .map((term, index) =>
      signedTerm(
        sourceName(byId.get(term.source) ?? { id: term.source }),
        term.coefficient,
        index === 0
      )
    )
    .join(" ")
}

export function describeRuleV2(rule: RuleV2): string {
  switch (rule.templateKind) {
    case "balance_equation": {
      const spec = rule.templateSpec
      return `${readableTerms(spec.sources, spec.terms)} = 0 · tolerance ${spec.tolerance} ${spec.sources[0]?.asset ?? ""}`
    }
    case "exchange_rate_bounds": {
      const spec = rule.templateSpec
      const byId = new Map(spec.sources.map((source) => [source.id, source]))
      const base = sourceName(
        byId.get(spec.baseSource) ?? { id: spec.baseSource }
      )
      const quote = sourceName(
        byId.get(spec.quoteSource) ?? { id: spec.quoteSource }
      )
      const bounds = isExplicitRate(spec.rate)
        ? `${spec.rate.min} ≤ rate ≤ ${spec.rate.max}`
        : `target ${spec.rate.target} ± ${spec.rate.toleranceBps} bps`
      return `${quote} per ${base} · ${bounds}`
    }
    case "source_consensus": {
      const spec = rule.templateSpec
      return `maximum observed balance − minimum observed balance ≤ ${spec.tolerance}`
    }
    case "coverage_ratio_bounds": {
      const spec = rule.templateSpec
      const numerator = readableTerms(spec.sources, spec.numeratorTerms)
      const denominator = readableTerms(spec.sources, spec.denominatorTerms)
      const bounds = isExplicitRate(spec.ratio)
        ? `${spec.ratio.min} ≤ ratio ≤ ${spec.ratio.max}`
        : `target ${spec.ratio.target} ± ${spec.ratio.toleranceBps} bps`
      return `(${numerator}) ÷ (${denominator}) · ${bounds}`
    }
    case "balance_bounds": {
      const spec = rule.templateSpec
      const assets = Object.keys(spec.bounds).sort()
      const shown = assets.slice(0, 3)
      const ranges = shown.map((asset) => {
        const { min, max } = spec.bounds[asset] ?? {}
        const body =
          min !== undefined && max !== undefined
            ? `${min}…${max}`
            : min !== undefined
              ? `≥ ${min}`
              : `≤ ${max}`
        return `${asset} ${body}`
      })
      const more = assets.length - shown.length
      return `${sourceName(spec.source)} within ${ranges.join(" · ")}${more > 0 ? ` · +${more} more` : ""}`
    }
    case "stale_holds": {
      const spec = rule.templateSpec
      const deadline = spec.deadline.expiryKey
        ? spec.deadline.createdKey
          ? `${spec.deadline.expiryKey}, else ${spec.deadline.createdKey} + ${spec.deadline.maxAge}`
          : spec.deadline.expiryKey
        : `${spec.deadline.createdKey} + ${spec.deadline.maxAge}`
      const window =
        spec.mode === "approaching"
          ? `deadline within the next ${spec.warnWithin}`
          : "deadline passed"
      const grain = spec.scope === "aggregate" ? "total held" : "per hold"
      return `${sourceName(spec.source)} · ${window} (${deadline}) · ${grain}`
    }
  }
}

export function backendSourcePath(
  validation: string | undefined,
  index: number
): string | undefined {
  return backendFieldPath(validation, `sources[${index}]`)
}

/** Return one backend validation message when it names the supplied machine path. */
export function backendFieldPath(
  validation: string | undefined,
  path: string
): string | undefined {
  if (!validation || !path) return undefined
  return validation.includes(path) ? validation : undefined
}

/** Validation details are preferred, but older wrappers may only preserve errorMessage. */
export function backendValidationText(
  error: { details?: string; message: string } | null | undefined
): string | undefined {
  return error?.details ?? error?.message
}
