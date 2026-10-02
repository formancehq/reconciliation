export type ReconTab =
  | "overview"
  | "rules"
  | "alerts"
  | "insights"
  | "audit"
  | "results"
export type ReconAlertFilter = "OPEN" | "ACKNOWLEDGED" | "RESOLVED" | "ALL"
export type ResultsSection = "days" | "run" | "queries" | "checks"

/** Results tab state. `day` is the day looked at: the run's day, and a query's `day` variable. */
export interface ResultsNav {
  rule?: string
  section?: ResultsSection
  day?: string
  run?: string
  /** A query file of tools/lettering-duckdb/queries, by name. */
  query?: string
  /** The `id` variable of a query (business-id). */
  id?: string
  /** Checks: the earlier run a chain check reads, when not the one `previousRun` names. */
  prev?: string
}

export interface ReconNav {
  tab: ReconTab
  ruleId?: string
  alertId?: string
  alertFilter?: ReconAlertFilter
  contractVersion?: 1 | 2
  results?: ResultsNav
}

const TABS = new Set<ReconTab>([
  "overview",
  "rules",
  "alerts",
  "insights",
  "audit",
  "results",
])
const RESULTS_SECTIONS = new Set<ResultsSection>(["days", "run", "queries", "checks"])
const RESULTS_KEYS = ["rule", "section", "day", "run", "query", "id", "prev"] as const
const ALERT_FILTERS = new Set<ReconAlertFilter>([
  "OPEN",
  "ACKNOWLEDGED",
  "RESOLVED",
  "ALL",
])

/** Parse a Reconcile hash without accepting unrelated app routes. */
export function parseReconHash(hash: string): ReconNav | null {
  const clean = hash.startsWith("#") ? hash.slice(1) : hash
  const [feature, query = ""] = clean.split("?")
  if (feature !== "reconcile") return null

  const params = new URLSearchParams(query)
  const requestedTab = params.get("view") as ReconTab | null
  const tab = requestedTab && TABS.has(requestedTab) ? requestedTab : "overview"

  if (tab === "rules") {
    const ruleId = params.get("rule") || undefined
    const contractVersion = params.get("contract") === "2" ? 2 : undefined
    return { tab, ruleId, contractVersion }
  }
  if (tab === "alerts") {
    const alertId = params.get("alert") || undefined
    const requestedFilter = params.get("status") as ReconAlertFilter | null
    const alertFilter =
      requestedFilter && ALERT_FILTERS.has(requestedFilter)
        ? requestedFilter
        : undefined
    const contractVersion = params.get("contract") === "2" ? 2 : undefined
    return { tab, alertId, alertFilter, contractVersion }
  }
  if (tab === "results") {
    const results: ResultsNav = {}
    for (const key of RESULTS_KEYS) {
      const value = params.get(key)
      if (value) (results as Record<string, string>)[key] = value
    }
    if (results.section && !RESULTS_SECTIONS.has(results.section))
      delete results.section
    return results.rule ? { tab, results } : { tab }
  }
  return { tab }
}

/** Canonical URL for a Reconcile navigation state. */
export function buildReconHash(nav: ReconNav): string {
  if (nav.tab === "overview") return "#reconcile"

  const params = new URLSearchParams({ view: nav.tab })
  if (nav.tab === "rules" && nav.ruleId) params.set("rule", nav.ruleId)
  if (nav.tab === "alerts") {
    if (nav.alertId) params.set("alert", nav.alertId)
    else if (nav.alertFilter) params.set("status", nav.alertFilter)
  }
  if (nav.tab === "results" && nav.results?.rule) {
    for (const key of RESULTS_KEYS) {
      const value = nav.results[key]
      if (value) params.set(key, value)
    }
  }
  if (nav.contractVersion === 2 && (nav.ruleId || nav.alertId))
    params.set("contract", "2")
  return `#reconcile?${params.toString()}`
}

export function sameReconNav(a: ReconNav, b: ReconNav): boolean {
  return (
    a.tab === b.tab &&
    a.ruleId === b.ruleId &&
    a.alertId === b.alertId &&
    a.alertFilter === b.alertFilter &&
    a.contractVersion === b.contractVersion &&
    RESULTS_KEYS.every((key) => a.results?.[key] === b.results?.[key])
  )
}
