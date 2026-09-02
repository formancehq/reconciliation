export type ReconTab = "overview" | "rules" | "alerts" | "insights" | "audit"
export type ReconAlertFilter = "OPEN" | "ACKNOWLEDGED" | "RESOLVED" | "ALL"

export interface ReconNav {
  tab: ReconTab
  ruleId?: string
  alertId?: string
  alertFilter?: ReconAlertFilter
  contractVersion?: 1 | 2
}

const TABS = new Set<ReconTab>([
  "overview",
  "rules",
  "alerts",
  "insights",
  "audit",
])
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
    a.contractVersion === b.contractVersion
  )
}
