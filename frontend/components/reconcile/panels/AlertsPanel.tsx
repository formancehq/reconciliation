"use client"

/**
 * Alerts inbox + detail + resolution actions.
 *
 * The break-management surface of the human-first workflow: triage open alerts,
 * inspect evidence, then acknowledge / resolve (fixed by booking) / accept
 * (business acceptance) / snooze. The detail's timeline is the alert's own
 * append-only event log, paged from GET /alerts/{id}/events (a projection of the
 * control-ledger activity stream) — see AlertEventsTimeline.
 */
import { Fragment, useCallback, useEffect, useState } from "react"
import {
  Bell,
  ArrowLeft,
  Loader2,
  Check,
  CircleCheck,
  Handshake,
  BellOff,
  BellRing,
  Layers,
  RefreshCw,
  X,
  ChevronDown,
  ChevronRight,
  Search,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog"
import { toast } from "@/components/ui/toast"
import {
  reconClientV2,
  useReconResource,
  poll,
  formatDateTime,
  formatRelative,
  evidenceEntries,
  evidenceLabel,
  findCaptureEvidenceOutcome,
  selectAlertCaptureEvidence,
  ReconError,
  SEVERITY_META,
  SEVERITY_ORDER,
  listAllAlerts,
  listAllRules,
  getAlert,
  getRule,
  listCaptures,
  listAlertEvents,
  contractVersionOf,
  resourceKey,
  ruleResourceKey,
  acknowledgeAlert,
  resolveAlert,
  acceptAlert,
  snoozeAlert,
  unsnoozeAlert,
  rememberReconActor,
  resolveReconActor,
  type AlertStatus,
  type AlertCaptureEvidenceSelection,
  type Alert,
  type Capture,
  type AlertEvent,
  type Cursor,
} from "@/lib/recon"
import { getConnectedUserEmail } from "@/lib/formance/helpers"
import { useReconNav } from "../ReconContext"
import {
  Loading,
  ErrorState,
  EmptyState,
  SeverityBadge,
  StatusBadge,
  VerdictBadge,
} from "../ui"
import createLogger from "@/lib/logger"
import {
  DescriptionDetails,
  DescriptionList,
  DescriptionTerm,
} from "@workspace/ui/components/description-list"
import {
  Item,
  ItemContent,
  ItemGroup,
  ItemHeader,
} from "@workspace/ui/components/item"
import { cn } from "@workspace/ui/lib/utils"
import { ReconFilterMenu } from "../ReconFilterMenu"
import { V2Evidence, isEvidence } from "../V2Evidence"
import { AlertEventsTimeline } from "../AlertEventsTimeline"

const log = createLogger("Recon")

export function AlertsPanel() {
  const { nav } = useReconNav()
  if (nav.alertId)
    return (
      <AlertDetail
        alertId={nav.alertId}
        contractVersion={nav.contractVersion ?? 1}
      />
    )
  return <AlertsInbox />
}

// ── Inbox ────────────────────────────────────────────────────────────────────
type StatusFilter = "ALL" | AlertStatus
export const DEFAULT_ALERT_FILTER: StatusFilter = "OPEN"
export type AlertSortKey = "latest" | "oldest" | "severity" | "occurrences"

interface InboxData {
  alerts: Alert[]
  ruleNames: Record<string, string>
}

const AUTO_REFRESH_MS = 10_000
const ALERTS_PAGE_SIZE = 20

export function filterAndSortAlerts(
  alerts: Alert[],
  ruleNames: Record<string, string>,
  options: {
    query: string
    status: StatusFilter
    ruleID: string
    severity: string
    sort: AlertSortKey
  }
) {
  const query = options.query.trim().toLowerCase()
  const filtered = alerts.filter(
    (alert) =>
      (options.status === "ALL" || alert.status === options.status) &&
      (options.ruleID === "all" ||
        alert.ruleID === options.ruleID ||
        ruleResourceKey(alert) === options.ruleID) &&
      (options.severity === "all" || alert.severity === options.severity) &&
      (!query ||
        alert.fingerprint.toLowerCase().includes(query) ||
        (
          ruleNames[ruleResourceKey(alert)] ??
          ruleNames[alert.ruleID] ??
          alert.ruleID
        )
          .toLowerCase()
          .includes(query))
  )

  return [...filtered].sort((a, b) => {
    const latestFirst =
      new Date(b.lastSeenAt).getTime() - new Date(a.lastSeenAt).getTime()
    if (options.sort === "oldest") return -latestFirst
    if (options.sort === "severity") {
      return (
        SEVERITY_ORDER.indexOf(a.severity) -
          SEVERITY_ORDER.indexOf(b.severity) || latestFirst
      )
    }
    if (options.sort === "occurrences")
      return b.occurrenceCount - a.occurrenceCount || latestFirst
    return latestFirst
  })
}

function AlertsInbox() {
  const { nav, goAlerts, openAlert, openRule, dataVersion, invalidate } =
    useReconNav()
  const filter: StatusFilter = nav.alertFilter ?? DEFAULT_ALERT_FILTER
  const [dialog, setDialog] = useState<{
    kind: ActionKind
    targets: Array<{ id: string; contractVersion: 1 | 2 }>
  } | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [grouped, setGrouped] = useState(false)
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [auto, setAuto] = useState(false)
  const [query, setQuery] = useState("")
  const [ruleFilter, setRuleFilter] = useState("all")
  const [severityFilter, setSeverityFilter] = useState("all")
  const [sort, setSort] = useState<AlertSortKey>("latest")
  const [page, setPage] = useState(1)

  const res = useReconResource<InboxData>(async (signal) => {
    const [alerts, rules] = await Promise.all([
      listAllAlerts(signal),
      listAllRules(signal),
    ])
    const ruleNames: Record<string, string> = {}
    rules.forEach((rule) => {
      ruleNames[`${contractVersionOf(rule)}:${rule.id}`] = rule.name
    })
    return { alerts, ruleNames }
  }, [dataVersion])

  // Auto-refresh: poll the inbox on an interval (invalidate → refetch).
  useEffect(() => {
    if (!auto) return
    const t = setInterval(() => invalidate(), AUTO_REFRESH_MS)
    return () => clearInterval(t)
  }, [auto, invalidate])

  const afterMutation = useCallback(async () => {
    await poll(() => listAllAlerts(), { tries: 3, intervalMs: 300 })
    setSelected(new Set())
    invalidate()
  }, [invalidate])

  if (res.loading) return <Loading label="Loading alerts…" />
  if (res.error) return <ErrorState error={res.error} onRetry={res.refetch} />

  const all = res.data?.alerts ?? []
  const ruleNames = res.data?.ruleNames ?? {}
  const counts = {
    ALL: all.length,
    OPEN: all.filter((a) => a.status === "OPEN").length,
    ACKNOWLEDGED: all.filter((a) => a.status === "ACKNOWLEDGED").length,
    RESOLVED: all.filter((a) => a.status === "RESOLVED").length,
  } as Record<StatusFilter, number>

  const visible = filterAndSortAlerts(all, ruleNames, {
    query,
    status: filter,
    ruleID: ruleFilter,
    severity: severityFilter,
    sort,
  })
  const ruleOptions: [string, string][] = [...new Set(all.map(ruleResourceKey))]
    .map((ruleKey): [string, string] => [
      ruleKey,
      ruleNames[ruleKey] ?? ruleKey,
    ])
    .sort((a, b) => a[1].localeCompare(b[1]))
  const pageCount = Math.max(1, Math.ceil(visible.length / ALERTS_PAGE_SIZE))
  const safePage = Math.min(page, pageCount)
  const pageStart = (safePage - 1) * ALERTS_PAGE_SIZE
  const pageAlerts = visible.slice(pageStart, pageStart + ALERTS_PAGE_SIZE)
  const pageSelectableIds = pageAlerts
    .filter((alert) => alert.status !== "RESOLVED")
    .map(resourceKey)
  const allSelected =
    pageSelectableIds.length > 0 &&
    pageSelectableIds.every((id) => selected.has(id))
  const resetList = () => {
    setPage(1)
    setSelected(new Set())
  }

  const toggleSel = (id: string) =>
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })
  const toggleAll = () =>
    setSelected((current) => {
      const next = new Set(current)
      pageSelectableIds.forEach((id) => {
        if (allSelected) next.delete(id)
        else next.add(id)
      })
      return next
    })
  const toggleGroup = (ids: string[]) =>
    setSelected((current) => {
      const next = new Set(current)
      const groupSelected = ids.every((id) => next.has(id))
      ids.forEach((id) => {
        if (groupSelected) next.delete(id)
        else next.add(id)
      })
      return next
    })
  const toggleCollapse = (rid: string) =>
    setCollapsed((s) => {
      const n = new Set(s)
      if (n.has(rid)) n.delete(rid)
      else n.add(rid)
      return n
    })

  const row = (a: Alert) => (
    <AlertListItem
      key={resourceKey(a)}
      alert={a}
      ruleName={ruleNames[ruleResourceKey(a)] ?? a.ruleID}
      selected={selected.has(resourceKey(a))}
      onSelect={() => toggleSel(resourceKey(a))}
      onOpen={() => openAlert(a.id, contractVersionOf(a))}
      onOpenRule={() => openRule(a.ruleID, contractVersionOf(a))}
      selectable={a.status !== "RESOLVED"}
    />
  )

  // Group this page by rule, preserving the newest-first order within each.
  const groups = new Map<string, Alert[]>()
  if (grouped)
    for (const a of pageAlerts) {
      const key = ruleResourceKey(a)
      const arr = groups.get(key)
      if (arr) arr.push(a)
      else groups.set(key, [a])
    }

  const selectedAlerts = all.filter((alert) => selected.has(resourceKey(alert)))
  const selectedAreActionable =
    selectedAlerts.length > 0 &&
    selectedAlerts.every((alert) => alert.status !== "RESOLVED")
  const selectedAreOpen =
    selectedAreActionable &&
    selectedAlerts.every((alert) => alert.status === "OPEN")
  const bulkActions: ActionKind[] = selectedAreOpen
    ? ["ack", "resolve", "accept", "snooze"]
    : selectedAreActionable
      ? ["resolve", "accept", "snooze"]
      : []

  return (
    <>
      <div className="flex min-h-full flex-col gap-3 p-3 sm:p-4">
        <div className="flex shrink-0 flex-wrap items-start gap-3">
          <div>
            <h2 className="text-sm font-semibold">Alerts</h2>
            <p className="text-xs text-muted-foreground">
              {all.length} alert{all.length === 1 ? "" : "s"} · triage and
              resolve breaks
            </p>
          </div>
          {res.refreshing && (
            <Loader2 className="ml-auto h-4 w-4 animate-spin text-muted-foreground" />
          )}
        </div>

        {/* Catalogue filters, then list operations. */}
        <div className="shrink-0 overflow-hidden rounded-md border">
          <div className="flex flex-wrap items-center gap-2 p-2">
            <div className="relative min-w-48 flex-1 basis-52">
              <Search className="pointer-events-none absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value)
                  resetList()
                }}
                placeholder="Search alerts or rules…"
                className="h-8 w-full pl-7"
                aria-label="Search alerts or rules"
              />
            </div>
            <ReconFilterMenu
              value={ruleFilter}
              onChange={(value) => {
                setRuleFilter(value)
                resetList()
              }}
              allLabel="All rules"
              width="w-full sm:w-48"
              options={ruleOptions}
            />
            <ReconFilterMenu
              value={severityFilter}
              onChange={(value) => {
                setSeverityFilter(value)
                resetList()
              }}
              allLabel="All severities"
              width="w-full sm:w-40"
              options={SEVERITY_ORDER.map((severity) => [
                severity,
                SEVERITY_META[severity].label,
              ])}
            />
            <ReconFilterMenu
              value={filter}
              onChange={(value) => {
                resetList()
                goAlerts(value as StatusFilter)
              }}
              allLabel={`All statuses (${counts.ALL})`}
              allValue="ALL"
              width="w-full sm:w-44"
              options={[
                ["OPEN", `Open (${counts.OPEN})`],
                ["ACKNOWLEDGED", `Acknowledged (${counts.ACKNOWLEDGED})`],
                ["RESOLVED", `Resolved (${counts.RESOLVED})`],
              ]}
            />
            <ReconFilterMenu
              value={sort}
              onChange={(value) => {
                setSort(value as AlertSortKey)
                resetList()
              }}
              allLabel="Sort: Latest"
              allValue="latest"
              width="w-full sm:w-40"
              options={[
                ["oldest", "Sort: Oldest"],
                ["severity", "Sort: Severity"],
                ["occurrences", "Sort: Observations"],
              ]}
            />
          </div>
          <div className="flex flex-wrap items-center gap-2 border-t px-2 py-1.5">
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              <Checkbox
                checked={allSelected}
                onCheckedChange={toggleAll}
                disabled={pageSelectableIds.length === 0}
                aria-label="Select this page"
              />
              Select page
            </label>
            <Button
              size="sm"
              variant={grouped ? "secondary" : "ghost"}
              onClick={() => setGrouped((group) => !group)}
            >
              <Layers className="mr-1.5 h-3.5 w-3.5" /> Group by rule
            </Button>
            <Button
              size="sm"
              variant={auto ? "secondary" : "ghost"}
              onClick={() => setAuto((enabled) => !enabled)}
              title={`Refresh every ${AUTO_REFRESH_MS / 1000}s`}
            >
              <RefreshCw
                className={`mr-1.5 h-3.5 w-3.5 ${auto ? "animate-spin" : ""}`}
              />{" "}
              Auto-refresh
            </Button>
            {selected.size > 0 && (
              <div className="ml-auto flex flex-wrap items-center gap-1.5">
                <span className="text-xs font-medium">
                  {selected.size} selected
                </span>
                {bulkActions.map((kind) => (
                  <Button
                    key={kind}
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      setDialog({
                        kind,
                        targets: selectedAlerts.map((alert) => ({
                          id: alert.id,
                          contractVersion: contractVersionOf(alert),
                        })),
                      })
                    }
                  >
                    {ACTION_META[kind].cta}
                  </Button>
                ))}
                <Button
                  size="icon-sm"
                  variant="ghost"
                  onClick={() => setSelected(new Set())}
                  aria-label="Clear selection"
                >
                  <X className="h-4 w-4" />
                </Button>
              </div>
            )}
          </div>
        </div>

        <div className="min-h-0 flex-1">
          {visible.length === 0 ? (
            <EmptyState
              icon={
                query ? (
                  <Search className="h-8 w-8" />
                ) : (
                  <Bell className="h-8 w-8" />
                )
              }
              title={
                query
                  ? "No alerts match"
                  : filter === "OPEN"
                    ? "No open alerts"
                    : "Nothing here"
              }
            >
              {query
                ? "Adjust the search or status filter."
                : filter === "OPEN"
                  ? "All clear — no open breaks right now."
                  : "No alerts match this filter."}
            </EmptyState>
          ) : grouped ? (
            [...groups.entries()].map(([rid, arr]) => {
              const selectableIds = arr
                .filter((alert) => alert.status !== "RESOLVED")
                .map(resourceKey)
              return (
                <section key={rid} className="mb-3">
                  <div className="flex items-center gap-2 rounded-lg border bg-muted/35 px-3 py-2">
                    <button
                      type="button"
                      onClick={() => toggleCollapse(rid)}
                      className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-left text-xs font-semibold"
                    >
                      <ChevronDown
                        className={`h-3.5 w-3.5 shrink-0 transition-transform ${collapsed.has(rid) ? "-rotate-90" : ""}`}
                      />
                      <span className="truncate">{ruleNames[rid] ?? rid}</span>
                      <span className="text-muted-foreground">
                        {arr.length}
                      </span>
                    </button>
                    <label className="flex cursor-pointer items-center gap-2 text-[11px] text-muted-foreground">
                      <Checkbox
                        checked={
                          selectableIds.length > 0 &&
                          selectableIds.every((id) => selected.has(id))
                            ? true
                            : selectableIds.some((id) => selected.has(id))
                              ? "indeterminate"
                              : false
                        }
                        onCheckedChange={() => toggleGroup(selectableIds)}
                        disabled={selectableIds.length === 0}
                        aria-label={`Select all alerts for ${ruleNames[rid] ?? rid}`}
                      />
                      <span className="hidden sm:inline">
                        Select rule alerts
                      </span>
                    </label>
                  </div>
                  {!collapsed.has(rid) && (
                    <ItemGroup className="mt-2 gap-2">{arr.map(row)}</ItemGroup>
                  )}
                </section>
              )
            })
          ) : (
            <ItemGroup className="gap-2">{pageAlerts.map(row)}</ItemGroup>
          )}
        </div>

        {visible.length > 0 && (
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t pt-3">
            <span className="text-xs text-muted-foreground">
              {pageStart + 1}–
              {Math.min(pageStart + ALERTS_PAGE_SIZE, visible.length)} of{" "}
              {visible.length} matching alert{visible.length === 1 ? "" : "s"}
            </span>
            <AlertsPagination
              page={safePage}
              pageCount={pageCount}
              onPageChange={setPage}
            />
          </div>
        )}
      </div>
      {dialog && (
        <AlertActionDialog
          kind={dialog.kind}
          targets={dialog.targets}
          onClose={() => setDialog(null)}
          onDone={async () => {
            setDialog(null)
            await afterMutation()
          }}
        />
      )}
    </>
  )
}

// A compact alert record: operational context in the header, latest evidence in
// the body. Header whitespace opens detail while controls keep explicit intent.
export function AlertListItem({
  alert: a,
  ruleName,
  selected,
  selectable,
  onSelect,
  onOpen,
  onOpenRule,
}: {
  alert: Alert
  ruleName: string
  selected: boolean
  onSelect: () => void
  selectable: boolean
  onOpen: () => void
  onOpenRule: () => void
}) {
  return (
    <Item
      role="listitem"
      variant="outline"
      data-selected={selected ? "true" : undefined}
      className={cn(
        "group block overflow-hidden rounded-lg p-0 shadow-xs transition-[border-color,box-shadow] hover:border-foreground/20 hover:shadow-sm",
        selected && "border-primary/40 ring-1 ring-primary/10"
      )}
    >
      <ItemHeader
        data-testid="alert-header"
        onClick={(event) => {
          const target = event.target
          if (
            target instanceof Element &&
            target.closest('button, a, input, label, [role="toolbar"]')
          )
            return
          onOpen()
        }}
        className="flex-wrap items-center border-b bg-muted/20 px-3 py-2.5 transition-colors hover:cursor-pointer hover:bg-muted/40"
      >
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <Checkbox
            checked={selected}
            onCheckedChange={onSelect}
            disabled={!selectable}
            aria-label={
              selectable ? "Select alert" : "Resolved alert cannot be selected"
            }
          />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={onOpen}
                aria-label={`Open alert ${a.fingerprint}`}
                className="group/alert flex min-w-0 cursor-pointer items-center gap-1.5 text-left"
              >
                <span className="truncate font-mono text-sm font-medium transition-colors group-hover/alert:text-primary">
                  {a.fingerprint}
                </span>
                <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-hover/alert:translate-x-0.5 group-hover/alert:text-primary" />
              </button>
              {a.snooze && isFuture(a.snooze.until) && (
                <BellOff
                  className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
                  aria-label="Snoozed"
                />
              )}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
              <SeverityBadge severity={a.severity} />
              <StatusBadge status={a.status} />
              <button
                type="button"
                onClick={onOpenRule}
                className="cursor-pointer truncate hover:text-primary hover:underline"
              >
                {ruleName}
              </button>
            </div>
          </div>
        </div>
        <div className="flex w-full shrink-0 flex-wrap items-center justify-end gap-2 sm:w-auto">
          <div className="min-w-28 text-right text-xs text-muted-foreground">
            <div>
              {a.occurrenceCount} observation
              {a.occurrenceCount === 1 ? "" : "s"}
            </div>
            <div title={formatDateTime(a.lastSeenAt)}>
              Latest {formatRelative(a.lastSeenAt)}
            </div>
          </div>
        </div>
      </ItemHeader>
      <ItemContent className="min-w-0 gap-0 px-3 py-2.5">
        <AlertEvidenceSummary evidence={a.evidence} />
      </ItemContent>
    </Item>
  )
}

const ALERT_EVIDENCE_META: Record<string, { label: string; priority: number }> =
  {
    asset: { label: "Asset", priority: 0 },
    balance: { label: "Observed balance", priority: 1 },
    leftBalance: { label: "Source A", priority: 1 },
    min: { label: "Minimum", priority: 2 },
    rightBalance: { label: "Source B", priority: 2 },
    max: { label: "Maximum", priority: 3 },
    signedDiff: { label: "Signed difference", priority: 3 },
    difference: { label: "Difference", priority: 4 },
    tolerance: { label: "Tolerance", priority: 5 },
  }

export function alertEvidenceFacts(evidence: unknown) {
  const entries = evidenceEntries(evidence)
  const hasSignedDifference = entries.some(
    (entry) => entry.key === "signedDiff"
  )
  const ranked = entries
    .filter((entry) => !(hasSignedDifference && entry.key === "difference"))
    .map((entry, index) => ({
      ...entry,
      index,
      meta: ALERT_EVIDENCE_META[entry.key],
    }))
    .filter((entry) => entry.meta)
    .sort((a, b) => a.meta.priority - b.meta.priority || a.index - b.index)
    .map(({ key, value, meta }) => ({ key, value, label: meta.label }))
  const rankedKeys = new Set(ranked.map((entry) => entry.key))
  const fallback = entries
    .filter(
      (entry) =>
        !rankedKeys.has(entry.key) &&
        !(hasSignedDifference && entry.key === "difference")
    )
    .map(({ key, value }) => ({ key, value, label: evidenceLabel(key) }))
  return [...ranked, ...fallback].slice(0, 5)
}

export function AlertEvidenceSummary({ evidence }: { evidence: unknown }) {
  if (isEvidence(evidence))
    return <V2Evidence evidence={evidence} compact passed={false} />
  const facts = alertEvidenceFacts(evidence)
  if (facts.length === 0)
    return (
      <p className="text-xs text-muted-foreground">
        No evidence recorded for the latest observation.
      </p>
    )

  const values = new Map(
    evidenceEntries(evidence).map(({ key, value }) => [key, value])
  )
  const asset = values.get("asset")
  const leftBalance = values.get("leftBalance")
  const rightBalance = values.get("rightBalance")

  if (leftBalance !== undefined && rightBalance !== undefined) {
    const difference =
      values.get("signedDiff") ?? values.get("difference") ?? "—"
    const tolerance = values.get("tolerance") ?? "0"
    return (
      <div>
        <ObservationHeading asset={asset} />
        <div className="grid items-stretch gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(10rem,auto)_minmax(0,1fr)]">
          <ObservationValue label="Source A balance" value={leftBalance} />
          <div className="flex min-h-16 flex-col items-center justify-center rounded-md border border-dashed bg-muted/15 px-3 py-2 text-center">
            <span className="text-[10px] tracking-wide text-muted-foreground uppercase">
              Difference
            </span>
            <span className="font-mono text-sm font-medium">{difference}</span>
            <span className="mt-0.5 text-[10px] text-muted-foreground">
              Allowed tolerance ±{tolerance} minor units
            </span>
          </div>
          <ObservationValue label="Source B balance" value={rightBalance} />
        </div>
      </div>
    )
  }

  const balance = values.get("balance")
  if (balance !== undefined && (values.has("min") || values.has("max"))) {
    return (
      <div>
        <ObservationHeading asset={asset} />
        <div className="grid gap-2 sm:grid-cols-2">
          <ObservationValue label="Observed balance" value={balance} />
          <div className="rounded-md border bg-muted/20 px-3 py-2">
            <div className="text-[10px] tracking-wide text-muted-foreground uppercase">
              Allowed range
            </div>
            <div className="mt-0.5 font-mono text-sm font-medium">
              {values.get("min") ?? "No minimum"} →{" "}
              {values.get("max") ?? "No maximum"}
            </div>
            <div className="text-[10px] text-muted-foreground">minor units</div>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div>
      <ObservationHeading asset={asset} />
      <dl className="grid gap-x-6 gap-y-2 sm:grid-cols-2 lg:grid-cols-5">
        {facts.map((fact) => (
          <div key={fact.key} className="min-w-0">
            <dt className="text-[10px] tracking-wide text-muted-foreground uppercase">
              {fact.label}
            </dt>
            <dd className="truncate font-mono text-xs" title={fact.value}>
              {fact.value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function ObservationHeading({ asset }: { asset?: string }) {
  return (
    <div className="mb-2 flex flex-wrap items-center gap-2">
      <span className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
        Latest observation
      </span>
      {asset && (
        <span className="rounded-full border bg-background px-2 py-0.5 font-mono text-[10px]">
          {asset}
        </span>
      )}
    </div>
  )
}

function ObservationValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border bg-muted/20 px-3 py-2">
      <div className="text-[10px] tracking-wide text-muted-foreground uppercase">
        {label}
      </div>
      <div
        className="mt-0.5 truncate font-mono text-sm font-medium"
        title={value}
      >
        {value}
      </div>
      <div className="text-[10px] text-muted-foreground">minor units</div>
    </div>
  )
}

export function AlertsPagination({
  page,
  pageCount,
  onPageChange,
}: {
  page: number
  pageCount: number
  onPageChange: (page: number) => void
}) {
  if (pageCount <= 1) return null

  return (
    <nav aria-label="Alerts pagination" className="flex items-center gap-2">
      <Button
        size="sm"
        variant="outline"
        className="h-8"
        disabled={page === 1}
        onClick={() => onPageChange(Math.max(1, page - 1))}
      >
        Previous
      </Button>
      <span className="min-w-20 text-center text-xs text-muted-foreground">
        Page {page} of {pageCount}
      </span>
      <Button
        size="sm"
        variant="outline"
        className="h-8"
        disabled={page === pageCount}
        onClick={() => onPageChange(Math.min(pageCount, page + 1))}
      >
        Next
      </Button>
    </nav>
  )
}

// ── Detail ────────────────────────────────────────────────────────────────────
type ActionKind = "ack" | "resolve" | "accept" | "snooze" | "unsnooze"
type CaptureLoadState =
  | { status: "ready"; captures: Capture[] }
  | { status: "error"; error: unknown }

// The alert timeline is now the alert's own event log (GET /alerts/{id}/events,
// a ledger projection) — see AlertEventsTimeline — instead of a client-side
// reconstruction from the rule-scoped stream.

function AlertDetail({
  alertId,
  contractVersion = 1,
}: {
  alertId: string
  contractVersion?: 1 | 2
}) {
  const { goAlerts, openRule, dataVersion, invalidate } = useReconNav()
  const [action, setAction] = useState<ActionKind | null>(null)

  const res = useReconResource<{
    alert: Alert
    ruleName?: string
    captureLoad: CaptureLoadState
  }>(async (signal) => {
    const alert = await getAlert(alertId, signal)
    const [ruleR, capsR] = await Promise.allSettled([
      getRule(alert.ruleID, signal),
      listCaptures(alert.ruleID, {
        period: alert.periodID,
        signal,
      }),
    ])
    if (ruleR.status === "rejected")
      log.debug("getRule for alert failed (rule may be gone)", {
        ruleId: alert.ruleID,
      })
    return {
      alert,
      ruleName: ruleR.status === "fulfilled" ? ruleR.value.name : undefined,
      captureLoad:
        capsR.status === "fulfilled"
          ? { status: "ready", captures: capsR.value }
          : { status: "error", error: capsR.reason },
    }
  }, [alertId, contractVersion, dataVersion])

  // The alert timeline is its own paginated event log (a ledger projection):
  // first page via useReconResource, then accumulate older pages via the cursor.
  const eventsRes = useReconResource<Cursor<AlertEvent>>(
    (signal) => listAlertEvents(alertId, undefined, signal),
    [alertId, contractVersion, dataVersion]
  )
  const eventsKey = `${contractVersion}:${alertId}:${dataVersion}`
  const [eventPages, setEventPages] = useState<{
    key: string
    older: AlertEvent[]
    next?: string
    hasMore?: boolean
  }>({ key: eventsKey, older: [] })
  if (eventPages.key !== eventsKey) setEventPages({ key: eventsKey, older: [] })
  const [loadingMoreEvents, setLoadingMoreEvents] = useState(false)
  const events = [...(eventsRes.data?.data ?? []), ...eventPages.older]
  const eventsNext = eventPages.next ?? eventsRes.data?.next
  const eventsHasMore = eventPages.hasMore ?? eventsRes.data?.hasMore ?? false
  const onLoadMoreEvents = async () => {
    if (!eventsNext || loadingMoreEvents) return
    setLoadingMoreEvents(true)
    try {
      const page = await listAlertEvents(alertId, eventsNext)
      setEventPages((cur) => ({
        ...cur,
        older: [...cur.older, ...(page.data ?? [])],
        next: page.next,
        hasMore: page.hasMore,
      }))
    } catch (err) {
      toast.error(
        err instanceof ReconError
          ? err.message
          : "Older events could not be loaded"
      )
    } finally {
      setLoadingMoreEvents(false)
    }
  }

  if (res.loading) return <Loading label="Loading alert…" />
  if (res.error) return <ErrorState error={res.error} onRetry={res.refetch} />
  if (!res.data) return null

  const { alert, ruleName, captureLoad } = res.data
  const captures = captureLoad.status === "ready" ? captureLoad.captures : []
  const evidence = alert.evidence
  const snoozed = !!alert.snooze && isFuture(alert.snooze.until)
  const isResolved = alert.status === "RESOLVED"
  // Correlate durable captures with the alert's exact rule, period, evaluation,
  // and fingerprint. A capture without a matching outcome is intentionally kept
  // as an empty latest evaluation rather than replaced with stale alert evidence.
  const latest = selectAlertCaptureEvidence(captures, alert)
  const history = captures.filter(
    (capture) =>
      capture.ruleID === alert.ruleID &&
      capture.periodID === alert.periodID &&
      findCaptureEvidenceOutcome(capture, alert.fingerprint)
  )

  // Inline evidence for the timeline: an alert event carries its evaluation id
  // but not the break evidence; recover it from the captures already loaded above
  // (this alert's fingerprint), so a fail/pass row expands to show what it saw.
  const capturesByEvaluation = new Map<string, Capture>()
  for (const capture of captures) {
    if (capture.evaluationID) capturesByEvaluation.set(capture.evaluationID, capture)
  }
  const eventEvidenceOutcome = (evaluationID?: string) => {
    if (!evaluationID) return undefined
    const capture = capturesByEvaluation.get(evaluationID)
    return capture ? findCaptureEvidenceOutcome(capture, alert.fingerprint) : undefined
  }
  const hasEventEvidence = (evaluationID?: string) => {
    const outcome = eventEvidenceOutcome(evaluationID)
    return (
      !!outcome &&
      (isEvidence(outcome.evidence) ||
        orderedEvidenceEntries(outcome.evidence).length > 0)
    )
  }
  const renderEventEvidence = (evaluationID?: string) => {
    const outcome = eventEvidenceOutcome(evaluationID)
    if (!outcome) return null
    if (isEvidence(outcome.evidence))
      return <V2Evidence evidence={outcome.evidence} compact passed={outcome.passed} />
    const entries = orderedEvidenceEntries(outcome.evidence)
    return entries.length > 0 ? <EvidenceDescription entries={entries} /> : null
  }

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="shrink-0 border-b px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="icon-sm"
            variant="ghost"
            onClick={() => goAlerts()}
            aria-label="Back to alerts"
          >
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <span className="min-w-0 font-mono text-sm break-all">
            {alert.fingerprint}
          </span>
          <SeverityBadge severity={alert.severity} />
          <StatusBadge status={alert.status} />
          {snoozed && (
            <span className="flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs text-muted-foreground">
              <BellOff className="h-3 w-3" /> snoozed until{" "}
              {formatDateTime(alert.snooze!.until)}
            </span>
          )}
          {res.refreshing && (
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <button
            type="button"
            onClick={() => openRule(alert.ruleID, contractVersion)}
            className="cursor-pointer hover:text-primary hover:underline"
          >
            Rule: {ruleName ?? alert.ruleID}
          </button>
          <span>·</span>
          <span>
            {alert.occurrenceCount}× occurrence
            {alert.occurrenceCount === 1 ? "" : "s"}
          </span>
          <span>·</span>
          <span title={formatDateTime(alert.firstSeenAt)}>
            first {formatRelative(alert.firstSeenAt)}
          </span>
          <span>·</span>
          <span title={formatDateTime(alert.lastSeenAt)}>
            last {formatRelative(alert.lastSeenAt)}
          </span>
        </div>
      </div>

      {/* Body */}
      <div className="min-h-0 flex-1 overflow-auto p-4">
        <div className="mx-auto max-w-3xl space-y-5">
          {/* Actions */}
          {!isResolved && (
            <div className="flex flex-wrap items-center gap-2">
              {alert.status === "OPEN" && (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setAction("ack")}
                >
                  <Check className="mr-1.5 h-3.5 w-3.5" /> Acknowledge
                </Button>
              )}
              <Button
                size="sm"
                variant="outline"
                onClick={() => setAction("resolve")}
              >
                <CircleCheck className="mr-1.5 h-3.5 w-3.5" /> Resolve
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => setAction("accept")}
              >
                <Handshake className="mr-1.5 h-3.5 w-3.5" /> Accept
              </Button>
              {snoozed ? (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setAction("unsnooze")}
                >
                  <BellRing className="mr-1.5 h-3.5 w-3.5" /> Unsnooze
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setAction("snooze")}
                >
                  <BellOff className="mr-1.5 h-3.5 w-3.5" /> Snooze
                </Button>
              )}
            </div>
          )}

          {captureLoad.status === "ready" ? (
            <EvaluationHistory
              alert={alert}
              latest={latest}
              history={history}
              currentEvidence={evidence}
            />
          ) : (
            <EvaluationHistoryUnavailable
              error={captureLoad.error}
              onRetry={res.refetch}
            />
          )}

          {/* Chronological journal: this alert's own append-only event log,
              paged from GET /alerts/{id}/events (a ledger projection). */}
          <AlertEventsTimeline
            events={events}
            hasMore={eventsHasMore}
            loading={eventsRes.loading}
            loadingMore={loadingMoreEvents}
            error={events.length === 0 ? eventsRes.error : undefined}
            onRetry={eventsRes.refetch}
            onLoadMore={onLoadMoreEvents}
            hasEvidence={hasEventEvidence}
            renderEvidence={renderEventEvidence}
          />

          {/* The ack / resolve / snooze narrative now lives in the Timeline
              above. The one fact it doesn't surface is the evidence frozen at
              business acceptance, so keep just that. */}
          {alert.resolution?.evidenceSnapshot && (
            <Section title="Evidence at resolution">
              <ResolutionEvidenceSnapshot
                evidence={alert.resolution.evidenceSnapshot}
              />
            </Section>
          )}
        </div>
      </div>

      {action && (
        <AlertActionDialog
          kind={action}
          targets={[{ id: alertId, contractVersion }]}
          onClose={() => setAction(null)}
          onDone={async () => {
            setAction(null)
            await poll<Alert>(
              async () => reconClientV2.getAlert(alertId),
              { tries: 3, intervalMs: 300 }
            )
            invalidate()
          }}
        />
      )}
    </div>
  )
}

function Section({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <section>
      <h3 className="mb-2 text-sm font-semibold">{title}</h3>
      {children}
    </section>
  )
}

export function EvaluationHistoryUnavailable({
  error,
  onRetry,
}: {
  error: unknown
  onRetry: () => void
}) {
  const message = error instanceof Error ? error.message : String(error)
  return (
    <Section title="Evaluation history">
      <Card
        role="alert"
        aria-label="Unable to load evaluation evidence"
        className="space-y-3 border-amber-foreground/30 p-4"
      >
        <div>
          <p className="text-sm font-medium">
            Unable to load evaluation evidence
          </p>
          <p className="mt-1 text-xs text-muted-foreground">{message}</p>
        </div>
        <Button size="sm" variant="outline" onClick={onRetry}>
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" />
          Retry evidence
        </Button>
      </Card>
    </Section>
  )
}

export function EvaluationHistory({
  alert,
  latest,
  history,
  currentEvidence,
}: {
  alert: Alert
  latest: AlertCaptureEvidenceSelection
  history: Capture[]
  currentEvidence: unknown
}) {
  const older = history.filter(
    (capture) => capture.evaluationID !== latest.capture?.evaluationID
  )
  const currentV2Evidence = isEvidence(currentEvidence)
    ? currentEvidence
    : undefined
  const currentEvidenceEntries = currentV2Evidence
    ? []
    : evidenceEntries(currentEvidence)
  const latestV2Evidence =
    latest.outcome && isEvidence(latest.outcome.evidence)
      ? latest.outcome.evidence
      : !latest.capture
        ? currentV2Evidence
        : undefined
  const latestEvidence = latest.outcome
    ? latestV2Evidence
      ? []
      : orderedEvidenceEntries(latest.outcome.evidence)
    : latest.capture
      ? []
      : currentEvidenceEntries
  const count =
    older.length +
    (latest.capture || currentV2Evidence || currentEvidenceEntries.length > 0
      ? 1
      : 0)

  return (
    <Section title={`Evaluation history · ${count}`}>
      <div className="space-y-3">
        <Card className="overflow-hidden border-primary/25">
          <div className="flex flex-wrap items-center gap-2 border-b bg-muted/35 px-3 py-2.5 text-xs text-muted-foreground">
            <span className="rounded-full bg-primary px-2 py-0.5 font-medium text-primary-foreground">
              Latest
            </span>
            {latest.capture ? (
              <>
                {latest.outcome && (
                  <VerdictBadge
                    verdict={latest.outcome.passed ? "pass" : "fail"}
                  />
                )}
                <span>
                  {latest.capture.trigger === "manual" ? "Manual" : "Scheduled"}
                </span>
                <span title={formatDateTime(latest.capture.capturedAt)}>
                  {formatRelative(latest.capture.capturedAt)}
                </span>
              </>
            ) : (
              // selectAlertCaptureEvidence found no capture for
              // alert.lastEvaluationID — retention pruned it, or it falls
              // outside the loaded page. The card then shows the evidence held
              // on the alert itself, and `older` cannot exclude a capture it
              // never matched, so the newest row below may be this same
              // evaluation. Say which it is rather than leaving the reader to
              // guess: the eval id on the right is the one to match against.
              <span>
                No capture retained for this evaluation — showing the evidence
                held on the alert
              </span>
            )}
            {alert.lastEvaluationID && (
              <span
                className="ml-auto font-mono"
                title={alert.lastEvaluationID}
              >
                eval {alert.lastEvaluationID.slice(0, 8)}
              </span>
            )}
          </div>
          <div className="p-3">
            {!latestV2Evidence && latestEvidence.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {latest.capture
                  ? "No evidence was captured for this fingerprint in this evaluation."
                  : "No capture or retained alert evidence was found for this evaluation."}
              </p>
            ) : (
              <>
                {latest.outcome?.passed && (
                  <div className="mb-3 flex items-center gap-1.5 text-sm font-medium text-green-700 dark:text-green-400">
                    <CircleCheck className="h-4 w-4" /> Successful resolution
                    evidence
                  </div>
                )}
                {latestV2Evidence ? (
                  <V2Evidence
                    evidence={latestV2Evidence}
                    passed={latest.outcome?.passed ?? false}
                  />
                ) : (
                  <EvidenceDescription entries={latestEvidence} />
                )}
              </>
            )}
          </div>
        </Card>

        {/* No "Earlier evaluations" heading: the section is already titled
            "Evaluation history", the first card is badged "Latest", and the list
            is chronological — so everything below it is earlier by
            construction. The heading restated that and broke one list into two
            apparent sections. */}
        {older.length > 0 && (
          <div className="space-y-2">
            {older.map((capture) => {
              const outcome = findCaptureEvidenceOutcome(
                capture,
                alert.fingerprint
              )
              return (
                <details
                  key={`${capture.transactionID}-${capture.evaluationID}`}
                  className="group rounded-md border bg-card"
                >
                  <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-xs text-muted-foreground [&::-webkit-details-marker]:hidden">
                    <ChevronDown className="h-3.5 w-3.5 shrink-0 -rotate-90 transition-transform group-open:rotate-0" />
                    {outcome && (
                      <VerdictBadge
                        verdict={outcome.passed ? "pass" : "fail"}
                      />
                    )}
                    <span>
                      {capture.trigger === "manual" ? "Manual" : "Scheduled"}
                    </span>
                    <span
                      className="ml-auto"
                      title={formatDateTime(capture.capturedAt)}
                    >
                      {formatRelative(capture.capturedAt)}
                    </span>
                  </summary>
                  <div className="border-t px-3 py-3">
                    {outcome ? (
                      isEvidence(outcome.evidence) ? (
                        <V2Evidence
                          evidence={outcome.evidence}
                          passed={outcome.passed}
                        />
                      ) : (
                        <EvidenceDescription
                          entries={orderedEvidenceEntries(outcome.evidence)}
                        />
                      )
                    ) : (
                      <p className="text-sm text-muted-foreground">
                        No evidence recorded.
                      </p>
                    )}
                  </div>
                </details>
              )
            })}
          </div>
        )}
      </div>
    </Section>
  )
}

const EVIDENCE_DETAIL_ORDER: Record<string, number> = {
  asset: 0,
  leftSource: 10,
  leftBalance: 11,
  difference: 20,
  signedDiff: 21,
  tolerance: 22,
  rightSource: 30,
  rightBalance: 31,
}

function orderedEvidenceEntries(evidence: unknown) {
  return evidenceEntries(evidence)
    .map((entry, index) => ({ ...entry, index }))
    .sort(
      (a, b) =>
        (EVIDENCE_DETAIL_ORDER[a.key] ?? 100) -
          (EVIDENCE_DETAIL_ORDER[b.key] ?? 100) || a.index - b.index
    )
    .map(({ key, value }) => ({ key, value }))
}

function EvidenceDescription({
  entries,
}: {
  entries: { key: string; value: string }[]
}) {
  return (
    <DescriptionList>
      {entries.map(({ key, value }) => (
        <Fragment key={key}>
          <DescriptionTerm className="text-muted-foreground">
            {evidenceLabel(key)}
          </DescriptionTerm>
          <DescriptionDetails className="text-sm break-all">
            {value}
          </DescriptionDetails>
        </Fragment>
      ))}
    </DescriptionList>
  )
}

export function ResolutionEvidenceSnapshot({
  evidence,
}: {
  evidence: unknown
}) {
  return (
    <div className="rounded-md border bg-muted/15 p-3">
      <div className="mb-2 text-xs font-medium text-muted-foreground">
        Immutable resolution evidence snapshot
      </div>
      {isEvidence(evidence) ? (
        <V2Evidence evidence={evidence} />
      ) : (
        <EvidenceDescription entries={orderedEvidenceEntries(evidence)} />
      )}
    </div>
  )
}

// ── Action dialog ─────────────────────────────────────────────────────────────
const ACTION_META: Record<
  ActionKind,
  { title: string; desc: string; cta: string }
> = {
  ack: {
    title: "Acknowledge alert",
    desc: "Mark as being investigated. Notifications keep flowing.",
    cta: "Acknowledge",
  },
  resolve: {
    title: "Resolve alert",
    desc: "The break is fixed. Add booking transaction refs to record a fix-by-booking.",
    cta: "Resolve",
  },
  accept: {
    title: "Accept alert",
    desc: "Business acceptance of a known break. A note is required.",
    cta: "Accept",
  },
  snooze: {
    title: "Snooze alert",
    desc: "Mute notifications until a future time.",
    cta: "Snooze",
  },
  unsnooze: {
    title: "Unsnooze alert",
    desc: "Resume notifications and record who lifted the snooze.",
    cta: "Unsnooze",
  },
}

export function AlertActionDialog({
  kind,
  targets,
  onClose,
  onDone,
}: {
  kind: ActionKind
  targets: Array<{ id: string; contractVersion: 1 | 2 }>
  onClose: () => void
  onDone: () => Promise<void> | void
}) {
  const meta = ACTION_META[kind]
  const n = targets.length
  const [by, setBy] = useState(() =>
    resolveReconActor(getConnectedUserEmail())
  )
  const [note, setNote] = useState("")
  const [refs, setRefs] = useState("")
  const [until, setUntil] = useState(defaultSnoozeUntil())
  const [busy, setBusy] = useState(false)

  const noteRequired = kind === "accept"
  const untilFuture = kind !== "snooze" || isFuture(until)
  const valid = !!by.trim() && (!noteRequired || !!note.trim()) && untilFuture
  const verb =
    kind === "ack"
      ? "acknowledged"
      : kind === "resolve"
        ? "resolved"
        : kind === "accept"
          ? "accepted"
          : kind === "snooze"
            ? "snoozed"
            : "unsnoozed"

  const applyOne = ({ id }: { id: string; contractVersion: 1 | 2 }) => {
    if (kind === "ack")
      return acknowledgeAlert(id, {
        by: by.trim(),
        note: note.trim() || undefined,
      })
    if (kind === "resolve") {
      const transactionRefs = refs
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean)
      return resolveAlert(id, {
        by: by.trim(),
        note: note.trim() || undefined,
        transactionRefs: transactionRefs.length ? transactionRefs : undefined,
      })
    }
    if (kind === "accept")
      return acceptAlert(id, {
        by: by.trim(),
        note: note.trim(),
      })
    if (kind === "unsnooze")
      return unsnoozeAlert(id, {
        by: by.trim(),
      })
    return snoozeAlert(id, {
      by: by.trim(),
      until: new Date(until).toISOString(),
      note: note.trim() || undefined,
    })
  }

  const submit = async () => {
    rememberReconActor(by)
    setBusy(true)
    const results = await Promise.allSettled(targets.map(applyOne))
    const ok = results.filter((r) => r.status === "fulfilled").length
    const fail = results.length - ok
    results.forEach((r) => {
      if (r.status === "rejected") {
        const e = r.reason
        log.error("alert action failed", {
          kind,
          error:
            e instanceof ReconError
              ? `${e.errorCode}: ${e.message}`
              : e instanceof Error
                ? e.message
                : String(e),
        })
      }
    })
    if (ok === 0) {
      const first = results.find((r) => r.status === "rejected") as
        PromiseRejectedResult | undefined
      toast.error(
        first?.reason instanceof ReconError
          ? first.reason.message
          : `Failed to ${kind} ${n} alert${n === 1 ? "" : "s"}`
      )
      setBusy(false)
      return
    }
    if (fail > 0) toast.warning(`${ok} ${verb}, ${fail} failed`)
    else toast.success(`${ok} alert${ok === 1 ? "" : "s"} ${verb}`)
    await onDone()
  }

  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o && !busy) onClose()
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {meta.title}
            {n > 1 ? ` · ${n} alerts` : ""}
          </DialogTitle>
          <DialogDescription>{meta.desc}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label htmlFor="recon-alert-actor" className="text-sm">Actor</Label>
            <Input
              id="recon-alert-actor"
              value={by}
              onChange={(e) => setBy(e.target.value)}
              placeholder="you@company.com"
            />
          </div>
          {kind === "resolve" && (
            <div className="space-y-1.5">
              <Label htmlFor="recon-alert-transaction-refs" className="text-sm">
                Booking transaction refs{" "}
                <span className="font-normal text-muted-foreground">
                  — comma-separated; sets “fixed by booking”
                </span>
              </Label>
              <Input
                id="recon-alert-transaction-refs"
                value={refs}
                onChange={(e) => setRefs(e.target.value)}
                placeholder="txn_123, txn_124"
              />
            </div>
          )}
          {kind === "snooze" && (
            <div className="space-y-1.5">
              <Label htmlFor="recon-alert-snooze-until" className="text-sm">Snooze until</Label>
              <Input
                id="recon-alert-snooze-until"
                type="datetime-local"
                value={until}
                onChange={(e) => setUntil(e.target.value)}
              />
              {!untilFuture && (
                <p className="text-xs text-destructive-foreground">
                  Must be in the future.
                </p>
              )}
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="recon-alert-note" className="text-sm">
              Note{" "}
              {noteRequired ? (
                <span className="text-destructive-foreground">*</span>
              ) : (
                <span className="font-normal text-muted-foreground">
                  (optional)
                </span>
              )}
            </Label>
            <Textarea
              id="recon-alert-note"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              rows={3}
              placeholder={
                noteRequired
                  ? "Why is this break acceptable?"
                  : "Context for the audit trail…"
              }
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!valid || busy}>
            {busy && <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />}
            {meta.cta}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ── helpers ────────────────────────────────────────────────────────────────
function isFuture(ts: string): boolean {
  const t = new Date(ts).getTime()
  return !Number.isNaN(t) && t > Date.now()
}

/** datetime-local value for now + 1 day (local time, no seconds). */
function defaultSnoozeUntil(): string {
  const d = new Date(Date.now() + 24 * 3600 * 1000)
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}
