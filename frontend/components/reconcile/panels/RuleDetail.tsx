"use client"

/** Rule detail is the canonical control record: applied config, then journal. */
import { useCallback, useState } from "react"
import {
  ArrowLeft,
  Play,
  Loader2,
  Pencil,
  Copy,
  Power,
  Trash2,
} from "lucide-react"
import { Button } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
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
  useReconResource,
  poll,
  templateLabel,
  PERIOD_TYPE_META,
  formatRelative,
  isZeroTime,
  ReconError,
  getRule,
  listRuleTimeline,
  evaluateRule,
  patchRuleEnabled,
  deleteRule,
  appendRuleActivities,
  evaluationActivityDetails,
  latestEvaluationActivity,
  recoverDeletedRule,
  type Rule,
  type RuleActivity,
  type Cursor,
} from "@/lib/recon"
import { useReconNav } from "../ReconContext"
import {
  Loading,
  ErrorState,
  SeverityBadge,
  ResultBadge,
} from "../ui"
import { V2RulePresentation } from "../V2RulePresentation"
import { CreateRuleDialog } from "./CreateRuleDialogV2"
import { RevisionValue, RuleTimeline } from "../RuleTimeline"
import createLogger from "@/lib/logger"

const log = createLogger("Recon")

export function RuleDetail({
  ruleId,
  contractVersion = 1,
}: {
  ruleId: string
  contractVersion?: 1 | 2
}) {
  const { goRules, openRule, openAlert, dataVersion, invalidate } =
    useReconNav()
  const [evaluating, setEvaluating] = useState(false)
  const [toggling, setToggling] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const paginationKey = `${ruleId}:${contractVersion}:${dataVersion}`
  const [pagination, setPagination] = useState<{
    key: string
    older: RuleActivity[]
    next?: string
    hasMore?: boolean
  }>({ key: paginationKey, older: [] })
  if (pagination.key !== paginationKey) {
    setPagination({ key: paginationKey, older: [] })
  }
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  const [paginationError, setPaginationError] = useState<unknown>()
  const [dialogMode, setDialogMode] = useState<"edit" | "duplicate" | null>(
    null
  )

  const ruleRes = useReconResource<Rule | null>(async (signal) => {
    try {
      return await getRule(ruleId, signal)
    } catch (error) {
      if (error instanceof ReconError && error.status === 404) return null
      throw error
    }
  }, [ruleId, contractVersion, dataVersion])

  const timelineRes = useReconResource<Cursor<RuleActivity>>(
    (signal) =>
      listRuleTimeline(ruleId, undefined, signal),
    [ruleId, contractVersion, dataVersion]
  )

  const baseActivities = timelineRes.data?.data ?? []
  const activities = appendRuleActivities(baseActivities, pagination.older)
  const nextCursor = pagination.next ?? timelineRes.data?.next
  const hasMore = pagination.hasMore ?? timelineRes.data?.hasMore ?? false

  const onEvaluate = useCallback(async () => {
    setEvaluating(true)
    try {
      const evaluation = await evaluateRule(ruleId)
      if (evaluation.result === "PASS") toast.success(`Evaluation → PASS`)
      else if (evaluation.result === "FAIL")
        toast.warning("Evaluation → FAIL", {
          description: "A break was recorded.",
        })
      else
        toast.error("Evaluation → ERROR", {
          description: evaluation.error?.slice(-160),
        })
      await poll(
        () => listRuleTimeline(ruleId),
        {
          tries: 3,
          intervalMs: 350,
          until: (page) =>
            (page.data ?? []).some(
              (activity) => activity.correlationID === evaluation.id
            ),
        }
      )
      invalidate()
    } catch (err) {
      log.error("evaluateRule failed", {
        ruleId,
        error:
          err instanceof ReconError
            ? `${err.errorCode}: ${err.message}`
            : err instanceof Error
              ? err.message
              : String(err),
      })
      toast.error(err instanceof ReconError ? err.message : "Evaluation failed")
    } finally {
      setEvaluating(false)
    }
  }, [ruleId, invalidate])

  if (ruleRes.loading && timelineRes.loading)
    return <Loading label="Loading control record…" />

  const deletedRule = recoverDeletedRule(activities, contractVersion)
  const rule = ruleRes.data ?? deletedRule
  const deleted = ruleRes.data === null
  const latestEvaluation = latestEvaluationActivity(activities)

  if (!rule && ruleRes.error && activities.length === 0)
    return <ErrorState error={ruleRes.error} onRetry={ruleRes.refetch} />

  const onToggle = async () => {
    if (!rule || deleted) return
    setToggling(true)
    try {
      await patchRuleEnabled(rule, !rule.enabled)
      toast.success(rule.enabled ? "Rule disabled" : "Rule enabled")
      invalidate()
    } catch (error) {
      toast.error(error instanceof ReconError ? error.message : "Update failed")
    } finally {
      setToggling(false)
    }
  }

  const onDelete = async () => {
    if (!rule || deleted) return
    setDeleting(true)
    try {
      await deleteRule(rule.id)
      toast.success("Rule deleted", { description: rule.name })
      setConfirmDelete(false)
      invalidate()
    } catch (error) {
      toast.error(error instanceof ReconError ? error.message : "Delete failed")
    } finally {
      setDeleting(false)
    }
  }

  const onLoadEarlier = async () => {
    if (!nextCursor || loadingEarlier) return
    setLoadingEarlier(true)
    try {
      const page = await listRuleTimeline(
        ruleId,
        nextCursor
      )
      setPagination((current) => ({
        ...current,
        older: appendRuleActivities(current.older, page.data ?? []),
        next: page.next,
        hasMore: page.hasMore,
      }))
      setPaginationError(undefined)
    } catch (error) {
      setPaginationError(error)
      toast.error(
        error instanceof ReconError
          ? error.message
          : "Earlier activity could not be loaded"
      )
    } finally {
      setLoadingEarlier(false)
    }
  }

  return (
    <>
      <div className="flex h-full flex-col">
        {/* Header */}
        <div className="shrink-0 border-b px-4 py-3">
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="icon-sm"
              variant="ghost"
              onClick={goRules}
              aria-label="Back to rules"
            >
              <ArrowLeft className="h-4 w-4" />
            </Button>
            <h2 className="min-w-0 text-base font-semibold break-words">
              {rule?.name ?? "Deleted rule"}
            </h2>
            {rule && <SeverityBadge severity={rule.severity} />}
            {deleted && (
              <span className="rounded-full border border-destructive-foreground/30 bg-destructive/10 px-2 py-0.5 text-xs text-destructive-foreground">
                Deleted rule
              </span>
            )}
            {rule && !deleted && !rule.enabled && (
              <span className="rounded-full border px-2 py-0.5 text-xs text-muted-foreground">
                Disabled
              </span>
            )}
            <div className="ml-auto flex flex-wrap items-center gap-2">
              {(ruleRes.refreshing || timelineRes.refreshing) && (
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
              )}
              {rule && <RuleDetailActions
                enabled={rule.enabled}
                deleted={deleted}
                evaluating={evaluating}
                toggling={toggling}
                onEdit={() => setDialogMode("edit")}
                onDuplicate={() => setDialogMode("duplicate")}
                onToggle={onToggle}
                onDelete={() => setConfirmDelete(true)}
                onEvaluate={onEvaluate}
              />}
            </div>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
            {rule && <span>{templateLabel(rule.templateKind)}</span>}
            {rule && <span>·</span>}
            {rule && <span>{PERIOD_TYPE_META[rule.periodType]?.label ?? rule.periodType}</span>}
            {rule && !isZeroTime(rule.updatedAt) && (
              <>
                <span>·</span>
                <span>updated {formatRelative(rule.updatedAt)}</span>
              </>
            )}
          </div>
        </div>

        {/* Body: current applied configuration, then the backend journal. */}
        <div className="min-h-0 flex-1 overflow-auto p-4">
          {rule ? (
            <CurrentRuleConfiguration
              rule={rule}
              deleted={deleted}
              latestEvaluation={latestEvaluation}
            />
          ) : (
            <section className="mb-6 rounded-md border border-dashed p-4">
              <h3 className="text-sm font-semibold">Deleted rule</h3>
              <p className="mt-1 text-sm text-muted-foreground">
                The current configuration is unavailable and no final snapshot
                has been loaded yet. The recorded timeline remains below.
              </p>
            </section>
          )}

          <RuleTimeline
            activities={activities}
            currentRevision={ruleRes.data?.revision}
            hasMore={hasMore}
            loading={timelineRes.loading}
            loadingEarlier={loadingEarlier}
            error={paginationError ?? timelineRes.error}
            onRetry={timelineRes.refetch}
            onLoadEarlier={() => void onLoadEarlier()}
            onOpenAlert={(alertID, version) => openAlert(alertID, version)}
          />
        </div>
      </div>
      {rule ? (
        <CreateRuleDialog
          key={`${dialogMode ?? "closed"}-${rule.id}`}
          open={dialogMode !== null}
          editRule={dialogMode === "edit" ? rule : null}
          duplicateRule={dialogMode === "duplicate" ? rule : null}
          onOpenChange={(open) => {
            if (!open) setDialogMode(null)
          }}
          onSaved={(savedRule) => {
            const wasDuplicate = dialogMode === "duplicate"
            setDialogMode(null)
            invalidate()
            if (wasDuplicate) openRule(savedRule.id, 2)
          }}
        />
      ) : null}
      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete rule &quot;{rule?.name ?? ruleId}&quot;?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This removes the current configuration. Its immutable rule
              activity timeline remains available as the control record.
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
    </>
  )
}

export function CurrentRuleConfiguration({
  rule,
  deleted,
  latestEvaluation,
}: {
  rule: Rule
  deleted?: boolean
  latestEvaluation?: Extract<RuleActivity, { kind: "evaluation.completed" }>
}) {
  const latest = latestEvaluation
    ? evaluationActivityDetails(latestEvaluation)
    : undefined
  const schedule = rule.schedule
    ? rule.schedule.kind === "cron"
      ? `${rule.schedule.expr ?? "Cron schedule"} (${rule.schedule.tz ?? "UTC"})`
      : "On demand"
    : "Not configured"

  return (
    <section
      aria-labelledby="current-configuration-heading"
      className="mb-6 min-w-0 rounded-md border"
    >
      <div className="border-b bg-muted/20 px-4 py-3">
        <div className="flex flex-wrap items-start gap-2">
          <div>
            <h3 id="current-configuration-heading" className="text-sm font-semibold">
              Current configuration
            </h3>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {deleted
                ? "Last-known configuration recovered from the final deletion snapshot."
                : "The configuration currently applied to new evaluations."}
            </p>
          </div>
          {deleted && (
            <span className="ml-auto rounded-full border px-2 py-0.5 text-xs font-medium">
              Read only · deleted
            </span>
          )}
        </div>
      </div>

      <dl className="grid gap-x-4 gap-y-3 border-b px-4 py-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
        <ConfigurationFact label="Rule" value={rule.name} />
        <ConfigurationFact
          label="State"
          value={deleted ? "Deleted" : rule.enabled ? "Enabled" : "Disabled"}
        />
        <ConfigurationFact label="Severity" value={rule.severity} />
        <ConfigurationFact
          label="Period type"
          value={PERIOD_TYPE_META[rule.periodType]?.label ?? rule.periodType}
        />
        <ConfigurationFact label="Schedule" value={schedule} />
        <div className="min-w-0">
          <dt className="text-[10px] tracking-wide text-muted-foreground uppercase">
            Latest result
          </dt>
          <dd className="mt-1">
            {latest ? (
              <ResultBadge result={latest.result} />
            ) : (
              <span className="text-muted-foreground">No evaluation recorded</span>
            )}
          </dd>
        </div>
        <div className="min-w-0 sm:col-span-2">
          <dt className="text-[10px] tracking-wide text-muted-foreground uppercase">
            Revision
          </dt>
          <dd className="mt-1">
            <RevisionValue revision={rule.revision} />
          </dd>
        </div>
      </dl>

      <div className="min-w-0 space-y-4 p-4">
        <V2RulePresentation rule={rule} sourcesFirst />

        {rule.compiledCEL && (
          <details className="rounded-md border text-xs">
            <summary className="cursor-pointer px-3 py-2 font-medium select-none">
              Implementation details
            </summary>
            <pre className="overflow-x-auto border-t px-3 py-2 font-mono text-[11px] whitespace-pre-wrap text-muted-foreground">
              {rule.compiledCEL}
            </pre>
          </details>
        )}
      </div>
    </section>
  )
}

function ConfigurationFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] tracking-wide text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="mt-1 break-words font-medium">{value}</dd>
    </div>
  )
}

export function RuleDetailActions({
  enabled,
  deleted = false,
  evaluating,
  toggling,
  onEdit,
  onDuplicate,
  onToggle,
  onDelete,
  onEvaluate,
}: {
  enabled: boolean
  deleted?: boolean
  evaluating: boolean
  toggling: boolean
  onEdit: () => void
  onDuplicate: () => void
  onToggle: () => void
  onDelete: () => void
  onEvaluate: () => void
}) {
  return (
    <div
      className="flex flex-wrap items-center gap-2"
      role="toolbar"
      aria-label="Rule details actions"
    >
      <Button size="sm" variant="outline" onClick={onEdit} disabled={deleted}>
        <Pencil className="mr-1.5 h-3.5 w-3.5" /> Edit
      </Button>
      <Button size="sm" variant="outline" onClick={onDuplicate}>
        <Copy className="mr-1.5 h-3.5 w-3.5" /> Duplicate
      </Button>
      <Button
        size="sm"
        variant="outline"
        onClick={onToggle}
        disabled={deleted || toggling}
      >
        {toggling ? (
          <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
        ) : (
          <Power className="mr-1.5 h-3.5 w-3.5" />
        )}
        {enabled ? "Disable" : "Enable"}
      </Button>
      <Button size="sm" variant="outline" onClick={onDelete} disabled={deleted}>
        <Trash2 className="mr-1.5 h-3.5 w-3.5" /> Delete
      </Button>
      <Button
        size="sm"
        onClick={onEvaluate}
        disabled={deleted || evaluating || !enabled}
        title={
          deleted
            ? "Deleted rules cannot be evaluated"
            : !enabled
              ? "Enable the rule to evaluate it"
              : undefined
        }
      >
        {evaluating ? (
          <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
        ) : (
          <Play className="mr-1.5 h-3.5 w-3.5" />
        )}
        Evaluate now
      </Button>
    </div>
  )
}
