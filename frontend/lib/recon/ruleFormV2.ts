import type { PeriodType, Schedule, Severity } from "./types"
import type {
  NamedSourceV2,
  RateBoundsV2,
  RuleRequestV2,
  RuleV2,
  TemplateKindV2,
} from "./typesV2"
import {
  compareDecimalStrings,
  DECIMAL_PATTERN,
  SIGNED_SAFE_INTEGER_PATTERN,
  SOURCE_ID_PATTERN,
  UNSIGNED_INTEGER_PATTERN,
} from "./v2"

export type PortfolioSideV2 = "numerator" | "denominator"
export type RateModeV2 = "explicit" | "target"

export interface RateBoundsDraftV2 {
  mode: RateModeV2
  min: string
  max: string
  target: string
  toleranceBps: string
}

export interface RuleFormDraftV2 {
  name: string
  kind: TemplateKindV2
  severity: Severity
  periodType: PeriodType
  schedule: Schedule
  enabled: boolean
  sources: NamedSourceV2[]
  coefficients: Record<string, string>
  portfolios: Record<string, PortfolioSideV2>
  tolerance: string
  baseSource: string
  quoteSource: string
  rate: RateBoundsDraftV2
}

export interface RuleFormIssueV2 {
  path: string
  message: string
}

export function emptyNamedSourceV2(index: number, ledger = ""): NamedSourceV2 {
  return {
    id: `source${index + 1}`,
    label: "",
    kind: "ledger",
    ledger,
    query: { $match: { address: "*" } },
    asset: "",
  }
}

export function defaultNamedSourcesV2(
  kind: TemplateKindV2,
  ledger = ""
): NamedSourceV2[] {
  const count =
    kind === "exchange_rate_bounds" ? 2 : kind === "balance_equation" ? 3 : 2
  return Array.from({ length: count }, (_, index) => ({
    ...emptyNamedSourceV2(index, ledger),
    asset: "USD/2",
  }))
}

export function nextNamedSourceIndexV2(sources: NamedSourceV2[]): number {
  const ids = new Set(sources.map((source) => source.id))
  let index = 0
  while (ids.has(`source${index + 1}`)) index += 1
  return index
}

export function addNamedSourceV2(
  sources: NamedSourceV2[],
  ledger = sources[0]?.ledger ?? ""
): NamedSourceV2[] {
  return [
    ...sources,
    emptyNamedSourceV2(nextNamedSourceIndexV2(sources), ledger),
  ]
}

export function removeNamedSourceV2(
  sources: NamedSourceV2[],
  index: number
): NamedSourceV2[] {
  return sources.filter((_, sourceIndex) => sourceIndex !== index)
}

export function moveNamedSourceV2(
  sources: NamedSourceV2[],
  from: number,
  to: number
): NamedSourceV2[] {
  if (from === to || from < 0 || from >= sources.length) return sources
  const next = [...sources]
  const [source] = next.splice(from, 1)
  if (!source) return sources
  next.splice(Math.max(0, Math.min(to, next.length)), 0, source)
  return next
}

export function createRuleFormDraftV2({
  rule,
  duplicate = false,
  activeLedger = "",
  kind = "balance_equation",
}: {
  rule?: RuleV2 | null
  duplicate?: boolean
  activeLedger?: string
  kind?: TemplateKindV2
} = {}): RuleFormDraftV2 {
  const selectedKind = rule?.templateKind ?? kind
  const sources = cloneSources(
    rule?.templateSpec.sources ??
      defaultNamedSourcesV2(selectedKind, activeLedger)
  )
  const coefficients = Object.fromEntries(
    sources.map((source, index) => [
      source.id,
      selectedKind === "balance_equation" && index === sources.length - 1
        ? "-1"
        : "1",
    ])
  )
  const portfolios = Object.fromEntries(
    sources.map((source, index) => [
      source.id,
      index === sources.length - 1 ? "denominator" : "numerator",
    ])
  ) as Record<string, PortfolioSideV2>

  if (rule?.templateKind === "balance_equation")
    for (const term of rule.templateSpec.terms)
      coefficients[term.source] = String(term.coefficient)
  if (rule?.templateKind === "coverage_ratio_bounds") {
    for (const term of rule.templateSpec.numeratorTerms) {
      coefficients[term.source] = String(term.coefficient)
      portfolios[term.source] = "numerator"
    }
    for (const term of rule.templateSpec.denominatorTerms) {
      coefficients[term.source] = String(term.coefficient)
      portfolios[term.source] = "denominator"
    }
  }

  const savedRate =
    rule?.templateKind === "exchange_rate_bounds"
      ? rule.templateSpec.rate
      : rule?.templateKind === "coverage_ratio_bounds"
        ? rule.templateSpec.ratio
        : undefined

  return {
    name: duplicate && rule ? `${rule.name} (copy)` : (rule?.name ?? ""),
    kind: selectedKind,
    severity: rule?.severity ?? "medium",
    periodType: rule?.periodType ?? "continuous",
    schedule: rule?.schedule ?? { kind: "on_demand" },
    enabled: rule?.enabled ?? true,
    sources,
    coefficients,
    portfolios,
    tolerance:
      rule?.templateKind === "balance_equation" ||
      rule?.templateKind === "source_consensus"
        ? rule.templateSpec.tolerance
        : "0",
    baseSource:
      rule?.templateKind === "exchange_rate_bounds"
        ? rule.templateSpec.baseSource
        : (sources[0]?.id ?? ""),
    quoteSource:
      rule?.templateKind === "exchange_rate_bounds"
        ? rule.templateSpec.quoteSource
        : (sources[1]?.id ?? ""),
    rate: {
      mode: savedRate && "target" in savedRate ? "target" : "explicit",
      min: savedRate && "min" in savedRate ? savedRate.min : "1.00",
      max: savedRate && "max" in savedRate ? savedRate.max : "1.10",
      target: savedRate && "target" in savedRate ? savedRate.target : "1.05",
      toleranceBps:
        savedRate && "toleranceBps" in savedRate
          ? String(savedRate.toleranceBps)
          : "100",
    },
  }
}

export function changeRuleTemplateV2(
  draft: RuleFormDraftV2,
  kind: TemplateKindV2,
  activeLedger = ""
): RuleFormDraftV2 {
  const next = createRuleFormDraftV2({
    activeLedger: activeLedger || draft.sources[0]?.ledger,
    kind,
  })
  return {
    ...next,
    name: draft.name,
    severity: draft.severity,
    periodType: draft.periodType,
    schedule: draft.schedule,
    enabled: draft.enabled,
  }
}

export function replaceRuleSourcesV2(
  draft: RuleFormDraftV2,
  sources: NamedSourceV2[]
): RuleFormDraftV2 {
  const ids = new Set(sources.map((source) => source.id))
  const coefficients = Object.fromEntries(
    sources.map((source) => [source.id, draft.coefficients[source.id] ?? "1"])
  )
  const portfolios = Object.fromEntries(
    sources.map((source) => [
      source.id,
      draft.portfolios[source.id] ?? "numerator",
    ])
  ) as Record<string, PortfolioSideV2>
  const available = sources.map((source) => source.id)
  const baseSource = ids.has(draft.baseSource)
    ? draft.baseSource
    : (available[0] ?? "")
  let quoteSource = ids.has(draft.quoteSource)
    ? draft.quoteSource
    : (available.find((id) => id !== baseSource) ?? "")
  if (baseSource === quoteSource) {
    quoteSource = available.find((id) => id !== baseSource) ?? ""
  }
  return {
    ...draft,
    sources,
    coefficients,
    portfolios,
    baseSource,
    quoteSource,
  }
}

export function renameRuleSourceV2(
  draft: RuleFormDraftV2,
  index: number,
  nextId: string
): RuleFormDraftV2 {
  const source = draft.sources[index]
  if (!source || source.id === nextId) return draft
  if (
    draft.sources.some(
      (candidate, candidateIndex) =>
        candidateIndex !== index && candidate.id === nextId
    )
  )
    return draft

  const previous = source.id
  const coefficients = { ...draft.coefficients }
  const portfolios = { ...draft.portfolios }
  coefficients[nextId] = coefficients[previous] ?? "1"
  portfolios[nextId] = portfolios[previous] ?? "numerator"
  delete coefficients[previous]
  delete portfolios[previous]

  return {
    ...draft,
    sources: draft.sources.map((candidate, candidateIndex) =>
      candidateIndex === index ? { ...candidate, id: nextId } : candidate
    ),
    coefficients,
    portfolios,
    baseSource: draft.baseSource === previous ? nextId : draft.baseSource,
    quoteSource: draft.quoteSource === previous ? nextId : draft.quoteSource,
  }
}

export function serializeRuleFormV2(draft: RuleFormDraftV2): RuleRequestV2 {
  const common = {
    name: draft.name.trim(),
    severity: draft.severity,
    periodType: draft.periodType,
    schedule: draft.schedule,
    enabled: draft.enabled,
  }
  if (draft.kind === "balance_equation")
    return {
      ...common,
      templateKind: draft.kind,
      templateSpec: {
        sources: cloneSources(draft.sources),
        terms: draft.sources.map((source) => ({
          source: source.id,
          coefficient: Number(draft.coefficients[source.id] ?? "1"),
        })),
        tolerance: draft.tolerance,
      },
    }
  if (draft.kind === "exchange_rate_bounds")
    return {
      ...common,
      templateKind: draft.kind,
      templateSpec: {
        sources: cloneSources(draft.sources),
        baseSource: draft.baseSource,
        quoteSource: draft.quoteSource,
        rate: serializeRate(draft.rate),
      },
    }
  if (draft.kind === "source_consensus")
    return {
      ...common,
      templateKind: draft.kind,
      templateSpec: {
        sources: cloneSources(draft.sources),
        tolerance: draft.tolerance,
      },
    }
  return {
    ...common,
    templateKind: draft.kind,
    templateSpec: {
      sources: cloneSources(draft.sources),
      numeratorTerms: termsForSide(draft, "numerator"),
      denominatorTerms: termsForSide(draft, "denominator"),
      ratio: serializeRate(draft.rate),
    },
  }
}

export function validateRuleFormV2(draft: RuleFormDraftV2): RuleFormIssueV2[] {
  const issues: RuleFormIssueV2[] = []
  const add = (path: string, message: string) => issues.push({ path, message })
  if (!draft.name.trim()) add("name", "Name is required.")
  if (draft.sources.length < 2 || draft.sources.length > 32)
    add("sources", "Use between 2 and 32 sources.")
  if (draft.kind === "exchange_rate_bounds" && draft.sources.length !== 2)
    add("sources", "Exchange-rate bounds requires exactly two sources.")

  const seen = new Set<string>()
  for (const [index, source] of draft.sources.entries()) {
    const prefix = `sources[${index}]`
    if (!SOURCE_ID_PATTERN.test(source.id))
      add(`${prefix}.id`, `Source ID must match ${SOURCE_ID_PATTERN.source}.`)
    if (seen.has(source.id)) add(`${prefix}.id`, "Source ID must be unique.")
    seen.add(source.id)
    if (!source.ledger.trim()) add(`${prefix}.ledger`, "Ledger is required.")
    if (!source.asset.trim()) add(`${prefix}.asset`, "Asset is required.")
    if (!source.query || Object.keys(source.query).length === 0)
      add(`${prefix}.query`, "Account query is required.")
    if (source.kind === "account_metadata" && !source.metadataKey.trim())
      add(`${prefix}.metadataKey`, "Metadata key is required.")
  }

  if (draft.kind !== "exchange_rate_bounds" && draft.sources.length > 0) {
    const asset = draft.sources[0]?.asset
    draft.sources.forEach((source, index) => {
      if (source.asset !== asset)
        add(
          `sources[${index}].asset`,
          "All sources must declare the same asset."
        )
    })
  }

  if (
    (draft.kind === "balance_equation" || draft.kind === "source_consensus") &&
    !UNSIGNED_INTEGER_PATTERN.test(draft.tolerance)
  )
    add(
      "tolerance",
      "Tolerance must be a non-negative integer string of at most 78 digits."
    )

  if (
    draft.kind === "balance_equation" ||
    draft.kind === "coverage_ratio_bounds"
  ) {
    for (const [index, source] of draft.sources.entries()) {
      const coefficient = draft.coefficients[source.id] ?? "1"
      const path = coefficientPath(draft, source.id, index)
      if (
        !SIGNED_SAFE_INTEGER_PATTERN.test(coefficient) ||
        !Number.isSafeInteger(Number(coefficient))
      )
        add(path, "Coefficient must be a non-zero signed safe integer.")
    }
  }

  if (draft.kind === "exchange_rate_bounds") {
    if (!seen.has(draft.baseSource))
      add("baseSource", "Choose a declared base source.")
    if (!seen.has(draft.quoteSource))
      add("quoteSource", "Choose a declared quote source.")
    if (draft.baseSource === draft.quoteSource)
      add("quoteSource", "Base and quote sources must be different.")
  }

  if (draft.kind === "coverage_ratio_bounds") {
    const sides = draft.sources.map(
      (source) => draft.portfolios[source.id] ?? "numerator"
    )
    if (!sides.includes("numerator"))
      add("numeratorTerms", "Assign at least one source to the numerator.")
    if (!sides.includes("denominator"))
      add("denominatorTerms", "Assign at least one source to the denominator.")
  }

  if (
    draft.kind === "exchange_rate_bounds" ||
    draft.kind === "coverage_ratio_bounds"
  ) {
    const prefix = draft.kind === "exchange_rate_bounds" ? "rate" : "ratio"
    if (draft.rate.mode === "explicit") {
      if (!DECIMAL_PATTERN.test(draft.rate.min))
        add(
          `${prefix}.min`,
          "Minimum must be a positive plain decimal with at most 18 fractional digits."
        )
      if (!DECIMAL_PATTERN.test(draft.rate.max))
        add(
          `${prefix}.max`,
          "Maximum must be a positive plain decimal with at most 18 fractional digits."
        )
      if (
        DECIMAL_PATTERN.test(draft.rate.min) &&
        DECIMAL_PATTERN.test(draft.rate.max) &&
        compareDecimalStrings(draft.rate.min, draft.rate.max) > 0
      )
        add(`${prefix}.max`, "Maximum must not be below minimum.")
    } else {
      if (!DECIMAL_PATTERN.test(draft.rate.target))
        add(
          `${prefix}.target`,
          "Target must be a positive plain decimal with at most 18 fractional digits."
        )
      if (
        !/^\d+$/.test(draft.rate.toleranceBps) ||
        Number(draft.rate.toleranceBps) > 10_000
      )
        add(
          `${prefix}.toleranceBps`,
          "Basis-point tolerance must be an integer from 0 through 10,000."
        )
    }
  }

  if (draft.schedule.kind === "cron" && !(draft.schedule.expr ?? "").trim())
    add("schedule.expr", "A cron expression is required.")
  return issues
}

export function coefficientPath(
  draft: RuleFormDraftV2,
  sourceId: string,
  sourceIndex = draft.sources.findIndex((source) => source.id === sourceId)
): string {
  if (draft.kind === "balance_equation")
    return `terms[${sourceIndex}].coefficient`
  const side = draft.portfolios[sourceId] ?? "numerator"
  const sideSources = draft.sources.filter(
    (source) => (draft.portfolios[source.id] ?? "numerator") === side
  )
  const termIndex = sideSources.findIndex((source) => source.id === sourceId)
  return `${side}Terms[${termIndex}].coefficient`
}

function termsForSide(draft: RuleFormDraftV2, side: PortfolioSideV2) {
  return draft.sources
    .filter((source) => (draft.portfolios[source.id] ?? "numerator") === side)
    .map((source) => ({
      source: source.id,
      coefficient: Number(draft.coefficients[source.id] ?? "1"),
    }))
}

function serializeRate(rate: RateBoundsDraftV2): RateBoundsV2 {
  return rate.mode === "explicit"
    ? { min: rate.min, max: rate.max }
    : { target: rate.target, toleranceBps: Number(rate.toleranceBps) }
}

function cloneSources(sources: NamedSourceV2[]): NamedSourceV2[] {
  return sources.map((source) => ({ ...source, query: { ...source.query } }))
}
