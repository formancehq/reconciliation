"use client"

/**
 * Reconcile ("Ledger Clarity") navigation + backend-health context.
 *
 * The recon surface is its own mini-app inside the SPA: a top-level tab
 * (Overview / Rules / Alerts) plus master→detail drill-downs (a selected rule
 * or alert). Reconcile owns the query portion of its `#reconcile` hash so
 * browser Back/Forward can restore tabs, filters, and detail drill-downs.
 *
 * It also owns the recon backend health probe (RECON_API_URL via /api/recon)
 * and a `dataVersion` counter that list views depend on, so a mutation in one
 * panel (create/evaluate/resolve…) makes the others refetch when revisited.
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react"
import {
  reconClient,
  getReconEndpoint,
  setReconEndpoint,
  type ReconEndpoint,
} from "@/lib/recon"
import {
  buildReconHash,
  parseReconHash,
  sameReconNav,
  type ReconAlertFilter,
  type ReconNav,
  type ReconTab,
  type ResultsNav,
} from "./reconNavigation"

export type {
  ReconAlertFilter,
  ReconNav,
  ReconTab,
  ResultsNav,
  ResultsSection,
} from "./reconNavigation"
export type HealthStatus = "checking" | "up" | "down"

interface ReconContextValue {
  /** The tab and drill-down shown: Overview until `navReady`. */
  nav: ReconNav
  /** False until the hash is read after mount: render no tab before. */
  navReady: boolean
  setTab: (tab: ReconTab) => void
  goOverview: () => void
  goRules: () => void
  openRule: (id: string, contractVersion?: 1 | 2) => void
  goAlerts: (filter?: ReconAlertFilter) => void
  openAlert: (id: string, contractVersion?: 1 | 2) => void
  /** Results tab: show a lettering rule, and optionally one day and run. */
  openResults: (results: ResultsNav) => void
  health: HealthStatus
  recheckHealth: () => void
  /** Increment to force list views to refetch. */
  dataVersion: number
  /** Call after any mutation so other panels refresh. */
  invalidate: () => void
  /** Which recon backend the UI talks to (empty url = server default). */
  endpoint: ReconEndpoint
  /** Switch the recon backend: persists, re-probes health, refetches views. */
  setEndpoint: (ep: ReconEndpoint) => void
}

const Ctx = createContext<ReconContextValue | null>(null)

const OVERVIEW: ReconNav = { tab: "overview" }

export function ReconProvider({ children }: { children: ReactNode }) {
  // The server sees neither the hash nor the tab's session storage, so the
  // first client render starts from the same state and the effects below read
  // both after mount. `nav` stays null until then: no panel mounts, and
  // fetches, for a tab the hash is about to replace.
  const [nav, setNav] = useState<ReconNav | null>(null)
  const navRef = useRef<ReconNav>(OVERVIEW)
  const [health, setHealth] = useState<HealthStatus>("checking")
  const [dataVersion, setDataVersion] = useState(0)
  const [endpoint, setEndpointState] = useState<ReconEndpoint>({ url: "" })
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const navigate = useCallback((next: ReconNav) => {
    if (sameReconNav(navRef.current, next)) return
    navRef.current = next
    window.history.pushState(
      { ...(window.history.state ?? {}) },
      "",
      buildReconHash(next)
    )
    setNav(next)
  }, [])

  // Read the entry's hash and canonicalize it without adding a Back step, then
  // restore internal navigation whenever browser history is traversed. Both
  // events are observed because pushState entries are browser-dependent here.
  useEffect(() => {
    const initial = parseReconHash(window.location.hash) ?? OVERVIEW
    navRef.current = initial
    setNav(initial)

    const desired = buildReconHash(initial)
    if (
      window.location.hash.startsWith("#reconcile") &&
      window.location.hash !== desired
    ) {
      window.history.replaceState(
        { ...(window.history.state ?? {}) },
        "",
        desired
      )
    }

    const applyHash = () => {
      const next = parseReconHash(window.location.hash)
      if (!next || sameReconNav(navRef.current, next)) return
      navRef.current = next
      setNav(next)
    }
    window.addEventListener("popstate", applyHash)
    window.addEventListener("hashchange", applyHash)
    return () => {
      window.removeEventListener("popstate", applyHash)
      window.removeEventListener("hashchange", applyHash)
    }
  }, [])

  const recheckHealth = useCallback(() => {
    setHealth("checking")
    void reconClient.health().then((ok) => {
      if (mounted.current) setHealth(ok ? "up" : "down")
    })
  }, [])

  // Initial probe on mount. Kept out of `recheckHealth` so we don't setState
  // ('checking') synchronously inside the effect — the default is already
  // 'checking', so we only flip once the async result lands.
  useEffect(() => {
    reconClient.health().then((ok) => {
      if (mounted.current) setHealth(ok ? "up" : "down")
    })
  }, [])

  // The client reads the endpoint from session storage on every call; only
  // its label waits for mount, like the hash.
  useEffect(() => {
    setEndpointState(getReconEndpoint())
  }, [])

  const setEndpoint = useCallback(
    (ep: ReconEndpoint) => {
      setReconEndpoint(ep)
      setEndpointState(getReconEndpoint())
      setDataVersion((v) => v + 1) // refetch every view against the new backend
      recheckHealth()
    },
    [recheckHealth]
  )

  const value = useMemo<ReconContextValue>(
    () => ({
      nav: nav ?? OVERVIEW,
      navReady: nav !== null,
      setTab: (tab) => navigate({ tab }),
      goOverview: () => navigate({ tab: "overview" }),
      goRules: () => navigate({ tab: "rules" }),
      openRule: (id, contractVersion) =>
        navigate({
          tab: "rules",
          ruleId: id,
          contractVersion: contractVersion === 2 ? 2 : undefined,
        }),
      goAlerts: (alertFilter) => navigate({ tab: "alerts", alertFilter }),
      openAlert: (id, contractVersion) =>
        navigate({
          tab: "alerts",
          alertId: id,
          contractVersion: contractVersion === 2 ? 2 : undefined,
        }),
      openResults: (results) => navigate({ tab: "results", results }),
      health,
      recheckHealth,
      dataVersion,
      invalidate: () => setDataVersion((v) => v + 1),
      endpoint,
      setEndpoint,
    }),
    [nav, navigate, health, recheckHealth, dataVersion, endpoint, setEndpoint]
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useReconNav(): ReconContextValue {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error("useReconNav must be used within <ReconProvider>")
  return ctx
}
