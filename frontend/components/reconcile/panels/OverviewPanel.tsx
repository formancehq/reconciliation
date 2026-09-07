"use client"

/**
 * Overview landing — the "observe" glance: how many breaks are open (by
 * severity), how the rule fleet looks, and the most recent alert activity.
 * Every tile/row is a shortcut into Rules or Alerts.
 *
 * (There is no global captures feed in the API — captures are per-rule — so
 * recent activity is derived from alerts; per-rule history lives in rule detail.)
 */
import { Bell, ListChecks, ShieldCheck, ArrowRight, Plus } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { cn } from "@workspace/ui/lib/utils"
import {
  useReconResource,
  listAllAlerts,
  listAllRules,
  contractVersionOf,
  resourceKey,
  SEVERITY_ORDER,
  formatRelative,
  type Alert,
  type Rule,
} from "@/lib/recon"
import { useReconNav } from "../ReconContext"
import { Loading, ErrorState, SeverityBadge, StatusBadge } from "../ui"

interface Data {
  alerts: Alert[]
  rules: Rule[]
}

export function OverviewPanel() {
  const { goAlerts, goRules, openAlert, dataVersion } = useReconNav()

  const res = useReconResource<Data>(async (signal) => {
    const [alerts, rules] = await Promise.all([
      listAllAlerts(signal),
      listAllRules(signal),
    ])
    return { alerts, rules }
  }, [dataVersion])

  if (res.loading) return <Loading label="Loading overview…" />
  if (res.error) return <ErrorState error={res.error} onRetry={res.refetch} />

  const alerts = res.data?.alerts ?? []
  const rules = res.data?.rules ?? []
  const ruleName = (alert: Alert) =>
    rules.find(
      (rule) =>
        rule.id === alert.ruleID &&
        contractVersionOf(rule) === contractVersionOf(alert)
    )?.name ?? alert.ruleID

  const open = alerts.filter((a) => a.status === "OPEN")
  const ack = alerts.filter((a) => a.status === "ACKNOWLEDGED")
  const resolved = alerts.filter((a) => a.status === "RESOLVED")
  const enabledRules = rules.filter((r) => r.enabled)

  const openBySeverity = SEVERITY_ORDER.map((s) => ({
    severity: s,
    count: open.filter((a) => a.severity === s).length,
  })).filter((x) => x.count > 0)

  const recent = recentOpenAlerts(alerts)

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-3 sm:p-4">
      {/* Stat tiles */}
      <div className="grid gap-3 sm:grid-cols-3">
        <Tile
          onClick={() => goAlerts("OPEN")}
          icon={<Bell className="h-4 w-4" />}
          label="Open alerts"
          value={open.length}
          accent={
            open.length > 0
              ? "text-destructive-foreground"
              : "text-green-foreground"
          }
        >
          {openBySeverity.length > 0 ? (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-xs text-muted-foreground">
                Highest severity
              </span>
              <SeverityBadge severity={openBySeverity[0].severity} />
            </div>
          ) : (
            <span className="text-xs text-muted-foreground">All clear</span>
          )}
        </Tile>

        <Tile
          onClick={() => goAlerts("RESOLVED")}
          icon={<ShieldCheck className="h-4 w-4" />}
          label="Handled"
          value={ack.length + resolved.length}
        >
          <span className="text-xs text-muted-foreground">
            {ack.length} acknowledged · {resolved.length} resolved
          </span>
        </Tile>

        <Tile
          onClick={goRules}
          icon={<ListChecks className="h-4 w-4" />}
          label="Rules"
          value={rules.length}
        >
          <span className="text-xs text-muted-foreground">
            {enabledRules.length} enabled · {rules.length - enabledRules.length}{" "}
            off
          </span>
        </Tile>
      </div>

      {rules.length === 0 && alerts.length === 0 && (
        <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed py-10 text-center">
          <p className="text-sm text-muted-foreground">
            No rules yet. Create one to start observing your ledgers.
          </p>
          <Button size="sm" onClick={goRules}>
            <Plus className="mr-1.5 h-4 w-4" /> Go to Rules
          </Button>
        </div>
      )}

      <div>
        <section>
          <SectionHead
            title="Recent open alerts"
            action={
              alerts.length > 0
                ? { label: "All alerts", onClick: () => goAlerts("ALL") }
                : undefined
            }
          />
          {recent.length === 0 ? (
            <p className="rounded-md border border-dashed px-3 py-8 text-center text-sm text-muted-foreground">
              No open alerts. All clear right now.
            </p>
          ) : (
            <ul className="divide-y rounded-md border">
              {recent.map((a) => (
                <li key={resourceKey(a)}>
                  <button
                    type="button"
                    onClick={() => openAlert(a.id, contractVersionOf(a))}
                    className="flex w-full flex-wrap items-center gap-3 px-3 py-2.5 text-left transition-colors hover:bg-accent"
                  >
                    <SeverityBadge severity={a.severity} />
                    <div className="min-w-40 flex-1 basis-40">
                      <div className="truncate font-mono text-sm">
                        {a.fingerprint}
                      </div>
                      <div className="truncate text-xs text-muted-foreground">
                        {ruleName(a)}
                      </div>
                    </div>
                    <StatusBadge status={a.status} />
                    <span
                      className="shrink-0 text-xs text-muted-foreground"
                      title={a.lastSeenAt}
                    >
                      {formatRelative(a.lastSeenAt)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  )
}

export function recentOpenAlerts(alerts: Alert[]) {
  return alerts
    .filter((alert) => alert.status === "OPEN")
    .sort(
      (a, b) =>
        new Date(b.lastSeenAt).getTime() - new Date(a.lastSeenAt).getTime()
    )
    .slice(0, 6)
}

function Tile({
  icon,
  label,
  value,
  accent,
  onClick,
  children,
}: {
  icon: React.ReactNode
  label: string
  value: number
  accent?: string
  onClick?: () => void
  children?: React.ReactNode
}) {
  // Card is a <div>; make it keyboard-activatable when it navigates (same
  // role="button" pattern used by the alert rows).
  const interactive = onClick
    ? {
        role: "button" as const,
        tabIndex: 0,
        onClick,
        onKeyDown: (e: React.KeyboardEvent) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault()
            onClick()
          }
        },
      }
    : {}
  return (
    <Card
      {...interactive}
      className={cn(
        "flex flex-col gap-2 p-4 text-left",
        onClick &&
          "cursor-pointer transition-colors hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
      )}
    >
      <div className="flex items-center gap-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">
        {icon}
        {label}
        {onClick && <ArrowRight className="ml-auto h-3.5 w-3.5 opacity-40" />}
      </div>
      <div className={cn("text-3xl font-semibold tabular-nums", accent)}>
        {value}
      </div>
      {children}
    </Card>
  )
}

function SectionHead({
  title,
  action,
}: {
  title: string
  action?: { label: string; onClick: () => void }
}) {
  return (
    <div className="mb-3 flex items-center justify-between">
      <h3 className="text-sm font-semibold">{title}</h3>
      {action && (
        <Button size="sm" variant="ghost" onClick={action.onClick}>
          {action.label} <ArrowRight className="ml-1 h-3.5 w-3.5" />
        </Button>
      )}
    </div>
  )
}
