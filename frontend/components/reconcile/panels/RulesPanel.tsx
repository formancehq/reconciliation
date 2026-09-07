"use client"

/**
 * Rules master view: the catalogue of reconciliation rules with contextual
 * actions and create-from-template. Selecting a rule drills into its detail
 * (capture history) via the shared recon nav.
 */
import { useCallback, useMemo, useState } from "react"
import {
  ListChecks,
  Plus,
  Play,
  Loader2,
  Search,
  ChevronRight,
  Trash2,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  Item,
  ItemContent,
  ItemGroup,
  ItemHeader,
} from "@workspace/ui/components/item"
import { toast } from "@/components/ui/toast"
import {
  reconClientV2,
  useReconResource,
  poll,
  describeAnyRule,
  formatRelative,
  PERIOD_TYPE_META,
  SEVERITY_ORDER,
  SEVERITY_META,
  ReconError,
  TEMPLATE_KINDS,
  TEMPLATE_META,
  templateLabel,
  listAllRules,
  listAllAlerts,
  listCaptures,
  patchRuleEnabled,
  evaluateRule,
  deleteRule,
  contractVersionOf,
  resourceKey,
  type Rule,
  type Capture,
  type Alert,
} from "@/lib/recon"
import { useReconNav } from "../ReconContext"
import {
  Loading,
  ErrorState,
  EmptyState,
  SeverityBadge,
  VerdictBadge,
} from "../ui"
import { RuleDetail } from "./RuleDetail"
import { V2RulePresentation } from "../V2RulePresentation"
import { CreateRuleDialog } from "./CreateRuleDialogV2"
import createLogger from "@/lib/logger"
import { FILTER_TOOLBAR } from "@/lib/uiClasses"
import { ReconFilterMenu } from "../ReconFilterMenu"

type SortKey = "name" | "severity" | "template"
type RuleRunSummary = { capture: Capture | null; alertID?: string }
const PAGE_SIZE = 10

const log = createLogger("Recon")

export function RulesPanel() {
  const { nav, openRule, openAlert, dataVersion, invalidate } = useReconNav()

  if (nav.ruleId)
    return (
      <RuleDetail
        ruleId={nav.ruleId}
        contractVersion={nav.contractVersion ?? 1}
      />
    )

  return (
    <RulesList
      onOpen={openRule}
      onOpenAlert={openAlert}
      dataVersion={dataVersion}
      invalidate={invalidate}
    />
  )
}

function RulesList({
  onOpen,
  onOpenAlert,
  dataVersion,
  invalidate,
}: {
  onOpen: (id: string, contractVersion?: 1 | 2) => void
  onOpenAlert: (id: string, contractVersion?: 1 | 2) => void
  dataVersion: number
  invalidate: () => void
}) {
  const rules = useReconResource<Rule[]>(
    (signal) => listAllRules(signal),
    [dataVersion]
  )
  const [evaluating, setEvaluating] = useState<Set<string>>(new Set())
  const [busyToggle, setBusyToggle] = useState<Set<string>>(new Set())
  const [creating, setCreating] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<Rule | null>(null)
  const [deleting, setDeleting] = useState(false)

  // Filters + sort (client-side; the list isn't server-paginated).
  const [query, setQuery] = useState("")
  const [fKind, setFKind] = useState<string>("all")
  const [fSeverity, setFSeverity] = useState<string>("all")
  const [fEnabled, setFEnabled] = useState<string>("all")
  const [sort, setSort] = useState<SortKey>("name")
  const [page, setPage] = useState(1)

  // Latest capture (verdict) per rule plus the alert associated with that
  // exact evaluation. Captures are fetched in parallel; alerts need one shared
  // fetch and are never linked by rule alone because that could open stale data.
  const idsKey = (rules.data ?? []).map(resourceKey).join(",")
  const verdicts = useReconResource<
    Record<string, RuleRunSummary>
  >(async (signal) => {
    const currentRules = rules.data ?? []
    const [entries, alerts] = await Promise.all([
      Promise.all(
        currentRules.map(async (rule) => {
          const caps = await listCaptures(
            rule.id,
            { signal }
          ).catch(() => [] as Capture[])
          return [resourceKey(rule), caps[0] ?? null] as const
        })
      ),
      listAllAlerts(signal).catch(() => [] as Alert[]),
    ])
    return Object.fromEntries(
      entries.map(([id, capture]) => {
        const alert = findAlertForCapture(alerts, capture)
        return [id, { capture, alertID: alert?.id }] as const
      })
    )
  }, [idsKey, dataVersion])

  const setBusy = (
    setter: React.Dispatch<React.SetStateAction<Set<string>>>,
    id: string,
    on: boolean
  ) =>
    setter((prev) => {
      const next = new Set(prev)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })

  const onToggle = useCallback(
    async (rule: Rule, enabled: boolean) => {
      const key = resourceKey(rule)
      setBusy(setBusyToggle, key, true)
      // Optimistic: flip locally, revert on failure.
      rules.setData((prev) =>
        (prev ?? []).map((r) =>
          resourceKey(r) === key ? { ...r, enabled } : r
        )
      )
      try {
        await patchRuleEnabled(rule, enabled)
        toast.success(`Rule ${enabled ? "enabled" : "disabled"}`)
      } catch (err) {
        log.error("patchRule failed", { ruleId: rule.id, enabled, err })
        rules.setData((prev) =>
          (prev ?? []).map((r) =>
            resourceKey(r) === key ? { ...r, enabled: !enabled } : r
          )
        )
        toast.error(
          err instanceof ReconError ? err.message : "Failed to update rule"
        )
      } finally {
        setBusy(setBusyToggle, key, false)
      }
    },
    [rules]
  )

  const onEvaluate = useCallback(
    async (rule: Rule) => {
      const key = resourceKey(rule)
      setBusy(setEvaluating, key, true)
      try {
        const evaluation = await evaluateRule(
          rule.id)
        const line = `“${rule.name}” → ${evaluation.result}`
        if (evaluation.result === "PASS") toast.success(line)
        else if (evaluation.result === "FAIL")
          toast.warning(line, {
            description: "A break was recorded — see Alerts.",
          })
        else
          toast.error(line, {
            description: describeEvalError(evaluation.error),
          })
        // Read-after-write is eventually consistent: let alerts/captures settle,
        // then refresh other panels.
        await poll<Alert[]>(async () => reconClientV2.listAlerts(), {
          tries: 3,
          intervalMs: 350,
        })
        invalidate()
      } catch (err) {
        log.error("evaluateRule failed", {
          ruleId: rule.id,
          error:
            err instanceof ReconError
              ? `${err.errorCode}: ${err.message}`
              : err instanceof Error
                ? err.message
                : String(err),
        })
        toast.error(
          err instanceof ReconError ? err.message : "Evaluation failed"
        )
      } finally {
        setBusy(setEvaluating, key, false)
      }
    },
    [invalidate]
  )

  const onDelete = async () => {
    if (!pendingDelete) return
    const deletedRule = pendingDelete
    setDeleting(true)
    try {
      await deleteRule(
        deletedRule.id)
      toast.success("Rule definition deleted", {
        description: "Opening its retained audit record.",
      })
      setPendingDelete(null)
      invalidate()
      onOpen(deletedRule.id, contractVersionOf(deletedRule))
    } catch (error) {
      toast.error(error instanceof ReconError ? error.message : "Delete failed")
    } finally {
      setDeleting(false)
    }
  }

  const total = rules.data?.length ?? 0
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    const out = (rules.data ?? []).filter(
      (r) =>
        (!q ||
          r.name.toLowerCase().includes(q) ||
          describeAnyRule(r).toLowerCase().includes(q)) &&
        (fKind === "all" || r.templateKind === fKind) &&
        (fSeverity === "all" || r.severity === fSeverity) &&
        (fEnabled === "all" || (fEnabled === "on") === r.enabled)
    )
    const cmp = (a: Rule, b: Rule) =>
      sort === "severity"
        ? SEVERITY_ORDER.indexOf(a.severity) -
          SEVERITY_ORDER.indexOf(b.severity)
        : sort === "template"
          ? a.templateKind.localeCompare(b.templateKind) ||
            a.name.localeCompare(b.name)
          : a.name.localeCompare(b.name)
    return [...out].sort(cmp)
  }, [rules.data, query, fKind, fSeverity, fEnabled, sort])

  const pageCount = Math.max(1, Math.ceil(visible.length / PAGE_SIZE))
  const safePage = Math.min(page, pageCount)
  const pageStart = (safePage - 1) * PAGE_SIZE
  const pageRules = visible.slice(pageStart, pageStart + PAGE_SIZE)

  if (rules.loading) return <Loading label="Loading rules…" />
  if (rules.error)
    return <ErrorState error={rules.error} onRetry={rules.refetch} />

  return (
    <div className="flex min-h-full flex-col gap-3 p-3 sm:p-4">
      <div className="flex shrink-0 flex-wrap items-start gap-3">
        <div>
          <h2 className="text-sm font-semibold">Rules</h2>
          <p className="text-xs text-muted-foreground">
            {total} rule{total === 1 ? "" : "s"} · observe ledgers, detect
            breaks
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {(rules.refreshing || verdicts.loading) && (
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          )}
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="mr-1.5 h-4 w-4" /> New rule
          </Button>
        </div>
      </div>

      {/* Filters / sort */}
      {total > 0 && (
        <div className={`${FILTER_TOOLBAR} shrink-0 gap-2 p-2`}>
          <div className="relative min-w-48 flex-1 basis-52">
            <Search className="pointer-events-none absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => {
                setQuery(e.target.value)
                setPage(1)
              }}
              placeholder="Search rules…"
              className="h-8 w-full pl-7"
            />
          </div>
          <ReconFilterMenu
            value={fKind}
            onChange={(v) => {
              setFKind(v)
              setPage(1)
            }}
            allLabel="All templates"
            width="w-full sm:w-40"
            options={TEMPLATE_KINDS.map(
              (k) => [k, TEMPLATE_META[k].label] as [string, string]
            )}
          />
          <ReconFilterMenu
            value={fSeverity}
            onChange={(v) => {
              setFSeverity(v)
              setPage(1)
            }}
            allLabel="All severities"
            width="w-full sm:w-40"
            options={SEVERITY_ORDER.map((s) => [s, SEVERITY_META[s].label])}
          />
          <ReconFilterMenu
            value={fEnabled}
            onChange={(v) => {
              setFEnabled(v)
              setPage(1)
            }}
            allLabel="All statuses"
            width="w-full sm:w-36"
            options={[
              ["on", "Enabled"],
              ["off", "Disabled"],
            ]}
          />
          <ReconFilterMenu
            value={sort}
            onChange={(v) => {
              setSort(v as SortKey)
              setPage(1)
            }}
            allLabel="Sort: Name"
            allValue="name"
            width="w-full sm:w-40"
            options={[
              ["severity", "Sort: Severity"],
              ["template", "Sort: Template"],
            ]}
          />
        </div>
      )}

      <div className="min-h-0 flex-1">
        {total === 0 ? (
          <EmptyState
            icon={<ListChecks className="h-8 w-8" />}
            title="No rules yet"
          >
            Create a rule from a template to start observing your ledgers.
          </EmptyState>
        ) : visible.length === 0 ? (
          <EmptyState
            icon={<Search className="h-8 w-8" />}
            title="No rules match"
          >
            Adjust the search or filters.
          </EmptyState>
        ) : (
          <ItemGroup className="gap-3">
            {pageRules.map((rule) => {
              const key = resourceKey(rule)
              const run = verdicts.data?.[key]
              const alertID = run?.alertID
              return (
                <RuleListItem
                  key={key}
                  rule={rule}
                  capture={run?.capture}
                  captureLoading={verdicts.loading && !verdicts.data}
                  evaluating={evaluating.has(key)}
                  toggling={busyToggle.has(key)}
                  onOpen={() => onOpen(rule.id, contractVersionOf(rule))}
                  onEvaluate={() => onEvaluate(rule)}
                  onToggle={(enabled) => onToggle(rule, enabled)}
                  onDelete={() => setPendingDelete(rule)}
                  onOpenAlert={
                    alertID
                      ? () => onOpenAlert(alertID, contractVersionOf(rule))
                      : undefined
                  }
                />
              )
            })}
          </ItemGroup>
        )}
      </div>

      {visible.length > 0 && (
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t pt-3">
          <span className="text-xs text-muted-foreground">
            {pageStart + 1}–{Math.min(pageStart + PAGE_SIZE, visible.length)} of{" "}
            {visible.length} matching rule{visible.length === 1 ? "" : "s"}
          </span>
          <RulesPagination
            page={safePage}
            pageCount={pageCount}
            onPageChange={setPage}
          />
        </div>
      )}

      <CreateRuleDialog
        key="new-v2"
        open={creating}
        onOpenChange={setCreating}
        onSaved={(rule) => {
          invalidate()
          onOpen(rule.id, 2)
        }}
      />
      <AlertDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open && !deleting) setPendingDelete(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete rule &quot;{pendingDelete?.name}&quot;?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This removes the active definition, so it will no longer appear
              in the Rules list. Its immutable timeline and final snapshot remain
              available as a read-only control record.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleting}
              onClick={(event) => {
                event.preventDefault()
                void onDelete()
              }}
            >
              {deleting && (
                <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
              )}
              Delete rule
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

export function RulesPagination({
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
    <nav aria-label="Rules pagination" className="flex items-center gap-2">
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

export function RuleListItem({
  rule,
  capture,
  captureLoading,
  evaluating,
  toggling,
  onOpen,
  onEvaluate,
  onToggle,
  onDelete,
  onOpenAlert,
}: {
  rule: Rule
  capture: Capture | null | undefined
  captureLoading: boolean
  evaluating: boolean
  toggling: boolean
  onOpen: () => void
  onEvaluate: () => void
  onToggle: (enabled: boolean) => void
  onDelete: () => void
  onOpenAlert?: () => void
}) {
  return (
    <Item
      role="listitem"
      variant="outline"
      className="block overflow-hidden rounded-lg p-0 shadow-xs transition-[border-color,box-shadow] hover:border-foreground/20 hover:shadow-sm"
    >
      <ItemHeader
        data-testid="rule-header"
        onClick={(event) => {
          const target = event.target
          if (
            target instanceof Element &&
            target.closest('button, a, input, label, [role="toolbar"]')
          )
            return
          onOpen()
        }}
        className="flex-wrap items-center border-b bg-muted/20 px-3 py-3 transition-colors hover:cursor-pointer hover:bg-muted/40 sm:px-4"
      >
        <div className="min-w-0 flex-1">
          <button
            type="button"
            onClick={onOpen}
            aria-label={`Open rule ${rule.name}`}
            className="group/rule flex max-w-full cursor-pointer items-center gap-1.5 text-left"
          >
            <span className="min-w-0 text-sm leading-snug font-medium break-words transition-colors group-hover/rule:text-primary">
              {rule.name}
            </span>
            <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-hover/rule:translate-x-0.5 group-hover/rule:text-primary" />
          </button>
          <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
            <SeverityBadge severity={rule.severity} />
            <span>{templateLabel(rule.templateKind)}</span>
            <span aria-hidden="true">·</span>
            <span>{PERIOD_TYPE_META[rule.periodType]?.label ?? rule.periodType}</span>
          </div>
        </div>

        <div className="flex w-full flex-wrap items-center gap-x-3 gap-y-2 sm:w-auto sm:justify-end">
          <div className="flex h-8 w-36 shrink-0 items-center justify-end text-foreground">
            <LastResult
              capture={capture}
              loading={captureLoading}
              onOpenAlert={onOpenAlert}
            />
          </div>
          <label className="flex h-8 w-[4.75rem] shrink-0 cursor-pointer items-center gap-2 text-xs whitespace-nowrap">
            <Switch
              checked={rule.enabled}
              disabled={toggling}
              onCheckedChange={onToggle}
              aria-busy={toggling}
              aria-label={
                rule.enabled
                  ? `Disable monitoring for ${rule.name}`
                  : `Enable monitoring for ${rule.name}`
              }
            />
            <span className="w-5 text-left">{rule.enabled ? "On" : "Off"}</span>
          </label>
          <div
            role="toolbar"
            aria-label={`Controls for ${rule.name}`}
            className="flex flex-wrap items-center justify-end gap-2"
          >
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="sm"
                  variant="outline"
                  className="w-28"
                  disabled={evaluating || !rule.enabled}
                  onClick={onEvaluate}
                >
                  {evaluating ? (
                    <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <Play className="mr-1.5 h-3.5 w-3.5" />
                  )}
                  {evaluating ? "Running…" : "Run now"}
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">
                {rule.enabled
                  ? "Run this reconciliation rule now"
                  : "Turn monitoring on before running this rule"}
              </TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  onClick={onDelete}
                  aria-label={`Delete ${rule.name}`}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom">Delete this rule</TooltipContent>
            </Tooltip>
          </div>
        </div>
      </ItemHeader>

      <ItemContent className="min-w-0 gap-3 p-3 sm:p-4">
        <V2RulePresentation rule={rule} compact />
      </ItemContent>
    </Item>
  )
}

/** Latest-capture cell for the rules table. */
function LastResult({
  capture,
  loading,
  onOpenAlert,
}: {
  capture: Capture | null | undefined
  loading: boolean
  onOpenAlert?: () => void
}) {
  if (loading)
    return (
      <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin text-muted-foreground" />
    )
  if (!capture)
    return <span className="px-2 text-xs text-muted-foreground">—</span>

  const summary = (
    <>
      <VerdictBadge verdict={capture.verdict} />
      <span
        className="border-l pl-2 text-xs text-muted-foreground"
        title={capture.capturedAt}
      >
        {formatRelative(capture.capturedAt)}
      </span>
      {onOpenAlert && (
        <ChevronRight className="h-3.5 w-3.5 text-muted-foreground transition-transform group-hover/result:translate-x-0.5 group-hover/result:text-foreground" />
      )}
    </>
  )

  if (!onOpenAlert)
    return (
      <span className="flex h-8 w-full items-center justify-end gap-2 px-2">
        {summary}
      </span>
    )

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          className="group/result h-8 w-full justify-end gap-2 px-2 font-normal"
          onClick={onOpenAlert}
          aria-label={`Open alert details for last ${capture.verdict.toLowerCase()} run`}
        >
          {summary}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="bottom">
        Open the alert raised by this run
      </TooltipContent>
    </Tooltip>
  )
}

export function findAlertForCapture(
  alerts: Alert[],
  capture: Capture | null | undefined
): Alert | undefined {
  if (!capture) return undefined
  return alerts.reduce<Alert | undefined>((latest, alert) => {
    if (contractVersionOf(alert) !== contractVersionOf(capture)) return latest
    if (
      alert.ruleID !== capture.ruleID ||
      alert.lastEvaluationID !== capture.evaluationID
    )
      return latest
    return !latest || alert.lastSeenAt > latest.lastSeenAt ? alert : latest
  }, undefined)
}

function describeEvalError(error: string | undefined): string | undefined {
  if (!error) return undefined
  // Errors can be verbose scout traces — keep the tail (the actual cause).
  return error.length > 160 ? `…${error.slice(-160)}` : error
}
