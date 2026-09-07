import type { PeriodType, Schedule, Severity } from "./types"
import type {
  HoldDeadlineV2,
  InstantEncodingV2,
  LedgerNamedSourceV2,
  NamedSourceV2,
  RateBoundsV2,
  RuleRequestV2,
  RuleV2,
  StaleHoldsModeV2,
  StaleHoldsScopeV2,
  TemplateKindV2,
} from "./typesV2"
import {
  compareDecimalStrings,
  DECIMAL_PATTERN,
  DURATION_PATTERN,
  SIGNED_SAFE_INTEGER_PATTERN,
  SOURCE_ID_PATTERN,
  UNSIGNED_INTEGER_PATTERN,
} from "./v2"

/** stale_holds reads one hold set, unlike the multi-source V2 templates. */
export const MAX_IDENTITY_KEYS_V2 = 8

/** A source declared as "every asset this account set holds". */
export const ASSET_WILDCARD_V2 = "*"

/** Signed whole number of minor units — mirrors the server's bounds parser. */
export const SIGNED_INTEGER_PATTERN_V2 = /^-?(0|[1-9][0-9]*)$/

/** Templates whose operation has a defined per-asset fan-out. */
export function supportsAllAssetsV2(kind: TemplateKindV2): boolean {
  return (
    kind === "balance_equation" ||
    kind === "source_consensus" ||
    kind === "coverage_ratio_bounds"
  )
}

export function allAssetsV2(draft: RuleFormDraftV2): boolean {
  return (
    draft.sources.length > 0 &&
    draft.sources.every((source) => source.asset === ASSET_WILDCARD_V2)
  )
}

/**
 * Flip every source between "every asset" and named assets. It is all-or-none
 * by design — a mixed spec has no defined alignment — so this is a spec-level
 * switch rather than a per-source one. Turning it off restores the assets each
 * source last had, so toggling is not lossy.
 */
export function setAllAssetsV2(
  draft: RuleFormDraftV2,
  on: boolean
): RuleFormDraftV2 {
  if (on) {
    const namedAssets = Object.fromEntries(
      draft.sources
        .filter((source) => source.asset !== ASSET_WILDCARD_V2)
        .map((source) => [source.id, source.asset])
    )
    return {
      ...draft,
      namedAssets: { ...draft.namedAssets, ...namedAssets },
      sources: draft.sources.map((source) => ({
        ...source,
        asset: ASSET_WILDCARD_V2,
      })),
    }
  }
  return {
    ...draft,
    sources: draft.sources.map((source) => ({
      ...source,
      asset: draft.namedAssets[source.id] ?? "USD/2",
    })),
  }
}

export type PortfolioSideV2 = "numerator" | "denominator"
export type RateModeV2 = "explicit" | "target"

export interface RateBoundsDraftV2 {
  mode: RateModeV2
  min: string
  max: string
  target: string
  toleranceBps: string
}

export interface HoldDeadlineDraftV2 {
  expiryKey: string
  createdKey: string
  encoding: InstantEncodingV2
  maxAge: string
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
  // stale_holds only. It reads a single hold set (sources[0]), so the shared
  // sources array carries exactly one entry for this kind.
  deadline: HoldDeadlineDraftV2
  mode: StaleHoldsModeV2
  warnWithin: string
  scope: StaleHoldsScopeV2
  identityKeys: string[]
  maxHoldsScanned: string
  /** Form-local: the asset each source carried before "every asset" was turned on. */
  namedAssets: Record<string, string>
  /** balance_bounds only: inclusive limits keyed by asset — and the declared universe. */
  bounds: BoundDraftV2[]
}

/** A row in the bounds editor. An empty side means unbounded, not zero. */
export interface BoundDraftV2 {
  asset: string
  min: string
  max: string
}

export const MAX_BOUNDS_ASSETS_V2 = 256

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
    kind === "stale_holds" || kind === "balance_bounds"
      ? 1
      : kind === "exchange_rate_bounds"
        ? 2
        : kind === "balance_equation"
          ? 3
          : 2
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
  const savedSources =
    rule?.templateKind === "stale_holds"
      ? [rule.templateSpec.source]
      : (rule?.templateSpec as { sources?: NamedSourceV2[] } | undefined)
          ?.sources
  const sources = cloneSources(
    savedSources ?? defaultNamedSourcesV2(selectedKind, activeLedger)
  )
  const savedHolds =
    rule?.templateKind === "stale_holds" ? rule.templateSpec : undefined
  const savedBounds =
    rule?.templateKind === "balance_bounds"
      ? Object.entries(rule.templateSpec.bounds)
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([asset, bound]) => ({
            asset,
            min: bound.min ?? "",
            max: bound.max ?? "",
          }))
      : undefined
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
    deadline: {
      expiryKey: savedHolds?.deadline.expiryKey ?? "",
      createdKey: savedHolds?.deadline.createdKey ?? "",
      encoding: savedHolds?.deadline.encoding ?? "datetime",
      maxAge: savedHolds?.deadline.maxAge ?? "",
    },
    mode: savedHolds?.mode ?? "stale",
    warnWithin: savedHolds?.warnWithin ?? "",
    scope: savedHolds?.scope ?? "per_hold",
    identityKeys: savedHolds?.identityKeys ? [...savedHolds.identityKeys] : [],
    maxHoldsScanned:
      savedHolds?.maxHoldsScanned === undefined
        ? ""
        : String(savedHolds.maxHoldsScanned),
    bounds: savedBounds ?? [{ asset: "USD/2", min: "", max: "" }],
    namedAssets: Object.fromEntries(
      sources
        .filter((source) => source.asset !== ASSET_WILDCARD_V2)
        .map((source) => [source.id, source.asset])
    ),
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
  if (draft.kind === "balance_bounds") {
    return {
      ...common,
      templateKind: draft.kind,
      templateSpec: {
        // One source, like stale_holds — the shared editor keeps it in sources[0].
        source: cloneSources(draft.sources)[0] as LedgerNamedSourceV2,
        bounds: Object.fromEntries(
          draft.bounds
            .filter((bound) => bound.asset.trim())
            .map((bound) => [
              bound.asset.trim(),
              {
                // An omitted side is unbounded; never send an empty string.
                ...(bound.min.trim() ? { min: bound.min.trim() } : {}),
                ...(bound.max.trim() ? { max: bound.max.trim() } : {}),
              },
            ])
        ),
      },
    }
  }
  if (draft.kind === "stale_holds") {
    const deadline: HoldDeadlineV2 = { encoding: draft.deadline.encoding }
    if (draft.deadline.expiryKey.trim())
      deadline.expiryKey = draft.deadline.expiryKey.trim()
    if (draft.deadline.createdKey.trim()) {
      deadline.createdKey = draft.deadline.createdKey.trim()
      deadline.maxAge = draft.deadline.maxAge.trim()
    }
    const identityKeys = draft.identityKeys
      .map((key) => key.trim())
      .filter(Boolean)
    const maxHoldsScanned = draft.maxHoldsScanned.trim()
    return {
      ...common,
      templateKind: draft.kind,
      templateSpec: {
        // stale_holds reads exactly one hold set; the shared editor keeps it in
        // sources[0]. cloneSources keeps the query object from being shared.
        source: cloneSources(draft.sources)[0] as LedgerNamedSourceV2,
        deadline,
        mode: draft.mode,
        scope: draft.scope,
        // Omitted rather than sent empty: the server rejects warnWithin in
        // stale mode, and treats an absent cap as "use the scope default".
        ...(draft.mode === "approaching" && draft.warnWithin.trim()
          ? { warnWithin: draft.warnWithin.trim() }
          : {}),
        ...(identityKeys.length ? { identityKeys } : {}),
        ...(maxHoldsScanned ? { maxHoldsScanned: Number(maxHoldsScanned) } : {}),
      },
    }
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
  if (draft.kind === "stale_holds" || draft.kind === "balance_bounds") {
    if (draft.kind === "stale_holds" && draft.sources.length !== 1)
      add("sources", "Stale holds reads exactly one hold set.")
    if (draft.sources[0]?.kind === "account_metadata")
      add(
        "sources[0].kind",
        "Stale holds reads held balances, so its source must be a ledger source."
      )
  } else if (draft.sources.length < 2 || draft.sources.length > 32)
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

  const wildcards = draft.sources.filter(
    (source) => source.asset === ASSET_WILDCARD_V2
  )
  if (wildcards.length > 0) {
    if (wildcards.length !== draft.sources.length)
      add(
        "sources",
        'Either every source checks all assets or none does — a mixed rule has no defined alignment.'
      )
    if (!supportsAllAssetsV2(draft.kind))
      add(
        "sources",
        draft.kind === "exchange_rate_bounds"
          ? "An exchange rate compares two named denominations, so it cannot check all assets."
          : "This template needs a named asset."
      )
    for (const [index, source] of draft.sources.entries())
      if (source.kind === "account_metadata")
        add(
          `sources[${index}].asset`,
          "A metadata source declares the one asset its key represents, so it cannot check all assets."
        )
  }

  if (
    draft.kind !== "exchange_rate_bounds" &&
    draft.kind !== "stale_holds" &&
    draft.kind !== "balance_bounds" &&
    wildcards.length === 0 &&
    draft.sources.length > 0
  ) {
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

  if (draft.kind === "balance_bounds") {
    if (draft.sources.length !== 1)
      add("sources", "Balance bounds reads exactly one account set.")
    if (draft.bounds.length === 0)
      add("bounds", "Add at least one asset to bound.")
    if (draft.bounds.length > MAX_BOUNDS_ASSETS_V2)
      add("bounds", `Bound at most ${MAX_BOUNDS_ASSETS_V2} assets.`)

    const declared = draft.sources[0]?.asset ?? ""
    const seenAssets = new Set<string>()
    draft.bounds.forEach((bound, index) => {
      const asset = bound.asset.trim()
      if (!asset) {
        add(`bounds[${index}].asset`, "Name the asset to bound.")
        return
      }
      if (asset === ASSET_WILDCARD_V2)
        add(
          `bounds[${index}].asset`,
          'A bound is denominated, so "*" is not an asset here — set it on the source instead.'
        )
      if (seenAssets.has(asset))
        add(`bounds[${index}].asset`, `${asset} is bounded twice.`)
      seenAssets.add(asset)

      const min = bound.min.trim()
      const max = bound.max.trim()
      if (!min && !max)
        add(
          `bounds[${index}].min`,
          "Set a minimum, a maximum, or both — a bound with neither never fails."
        )
      for (const [side, value] of [["min", min], ["max", max]])
        if (value && !SIGNED_INTEGER_PATTERN_V2.test(value))
          add(
            `bounds[${index}].${side}`,
            "Use a whole number of minor units; negatives are allowed."
          )
      if (min && max && SIGNED_INTEGER_PATTERN_V2.test(min) && SIGNED_INTEGER_PATTERN_V2.test(max) && BigInt(min) > BigInt(max))
        add(`bounds[${index}].max`, "The maximum must not be below the minimum.")
    })

    if (declared && declared !== ASSET_WILDCARD_V2) {
      const only = draft.bounds[0]?.asset.trim()
      if (draft.bounds.length !== 1 || only !== declared)
        add(
          "bounds",
          `The source declares ${declared}, so bound exactly that asset — switch it to every asset to bound several.`
        )
    }
  }

  if (draft.kind === "stale_holds") {
    const expiryKey = draft.deadline.expiryKey.trim()
    const createdKey = draft.deadline.createdKey.trim()
    const maxAge = draft.deadline.maxAge.trim()
    if (!expiryKey && !createdKey)
      add(
        "deadline.expiryKey",
        "Give the deadline an expiry key, a creation key, or both."
      )
    if (createdKey && !maxAge)
      add(
        "deadline.maxAge",
        "A maximum age is required when holds are dated from their creation."
      )
    if (maxAge && !DURATION_PATTERN.test(maxAge))
      add("deadline.maxAge", 'Use a duration such as "48h" or "90m".')

    const warnWithin = draft.warnWithin.trim()
    if (draft.mode === "approaching" && !warnWithin)
      add(
        "warnWithin",
        "A warning window is required when the rule watches holds approaching their deadline."
      )
    if (warnWithin && !DURATION_PATTERN.test(warnWithin))
      add("warnWithin", 'Use a duration such as "6h" or "90m".')

    const identitySeen = new Set<string>()
    draft.identityKeys.forEach((key, index) => {
      const trimmed = key.trim()
      if (!trimmed) {
        add(`identityKeys[${index}]`, "Remove the empty label key.")
        return
      }
      if (identitySeen.has(trimmed))
        add(`identityKeys[${index}]`, `${trimmed} is listed twice.`)
      identitySeen.add(trimmed)
    })
    if (draft.identityKeys.length > MAX_IDENTITY_KEYS_V2)
      add("identityKeys", `Use at most ${MAX_IDENTITY_KEYS_V2} label keys.`)

    const cap = draft.maxHoldsScanned.trim()
    if (cap && (!/^\d+$/.test(cap) || Number(cap) < 1))
      add("maxHoldsScanned", "The hold limit must be a positive whole number.")
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
