"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import { AlertTriangle, FlaskConical, Loader2, Wand2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { toast } from "@/components/ui/toast"
import { useLedgerClient } from "@/lib/connection/provider"
import createLogger from "@/lib/logger"
import {
  allAssets,
  backendValidationText,
  captureEvidenceOutcomes,
  changeRuleTemplate,
  createRuleFormDraft,
  ReconError,
  reconClient,
  renameRuleSource,
  replaceRuleSources,
  SEVERITY_META,
  SEVERITY_ORDER,
  serializeRuleForm,
  setAllAssets,
  supportsAllAssets,
  TEMPLATE_KINDS,
  V3_TEMPLATES,
  TEMPLATE_META,
  validateRuleForm,
  type RuleFormDraft,
  type RulePreview,
  evidenceGap,
  type Rule,
  type Severity,
  type TemplateKind,
} from "@/lib/recon"
import { useChartStore } from "@/stores/chartStore"
import { NamedSourcesEditor } from "../NamedSourcesEditor"
import {
  BalanceBoundsEditor,
  BalanceEquationEditor,
  CoverageRatioBoundsEditor,
  ExchangeRateBoundsEditor,
  SourceConsensusEditor,
  StaleHoldsEditor,
} from "../templates/TemplateEditors"
import { RunTimingFields } from "../RunTimingFields"
import { EvidenceView, isEvidence } from "../EvidenceView"

const log = createLogger("Recon")

/** Templates that read exactly one account set rather than comparing several. */
function singleSource(kind: TemplateKind): boolean {
  return kind === "stale_holds" || kind === "balance_bounds"
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  editRule?: Rule | null
  duplicateRule?: Rule | null
  onSaved: (rule: Rule) => void
}

export function CreateRuleDialog({
  open,
  editRule,
  duplicateRule,
  ...props
}: Props) {
  if (!open) return null
  const modeKey = editRule
    ? `edit:${editRule.id}`
    : duplicateRule
      ? `duplicate:${duplicateRule.id}`
      : "create"
  return (
    <CreateRuleDialogOpen
      key={modeKey}
      {...props}
      open
      editRule={editRule}
      duplicateRule={duplicateRule}
    />
  )
}

function CreateRuleDialogOpen({
  open,
  onOpenChange,
  editRule,
  duplicateRule,
  onSaved,
}: Props) {
  const seed = editRule ?? duplicateRule
  const isEdit = !!editRule
  const isDuplicate = !editRule && !!duplicateRule
  const activeLedger = useChartStore((state) => state.selectedLedger).trim()
  const [draft, setDraft] = useState<RuleFormDraft>(() =>
    createRuleFormDraft({
      rule: seed,
      duplicate: isDuplicate,
      activeLedger,
    })
  )
  const [submitting, setSubmitting] = useState(false)
  const [serverError, setServerError] = useState<ReconError | null>(null)
  const [ledgerOptions, setLedgerOptions] = useState<string[]>([])
  const ledgerClient = useLedgerClient()

  useEffect(() => {
    if (!open || !ledgerClient) return
    let alive = true
    ledgerClient
      .listLedgers()
      .then((ledgers) => {
        if (alive) setLedgerOptions(ledgers.map((ledger) => ledger.name))
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [open, ledgerClient])

  const issues = useMemo(() => validateRuleForm(draft), [draft])
  const backendError = backendValidationText(serverError)

  // A preview belongs to the configuration it tested: any edit makes it stale.
  const [preview, setPreview] = useState<{ key: string; result: RulePreview } | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const configKey = useMemo(() => {
    try {
      const request = serializeRuleForm(draft)
      return JSON.stringify([request.templateKind, request.templateSpec])
    } catch {
      return ""
    }
  }, [draft])
  const previewStale = !!preview && preview.key !== configKey

  const runPreview = async () => {
    setPreviewing(true)
    setServerError(null)
    try {
      const request = serializeRuleForm(draft)
      const result = await reconClient.previewRule({
        templateKind: request.templateKind,
        templateSpec: request.templateSpec,
      })
      setPreview({ key: configKey, result })
    } catch (error) {
      log.error("Rule preview failed", { error })
      setPreview(null)
      if (error instanceof ReconError) setServerError(error)
      else toast.error("Failed to test the rule")
    } finally {
      setPreviewing(false)
    }
  }

  const changeKind = (kind: TemplateKind) => {
    setDraft((current) => changeRuleTemplate(current, kind, activeLedger))
    setServerError(null)
  }

  const submit = async () => {
    setSubmitting(true)
    setServerError(null)
    try {
      const request = serializeRuleForm(draft)
      const rule =
        isEdit && editRule
          ? await reconClient.patchRule(editRule.id, {
              name: request.name,
              templateKind: request.templateKind,
              templateSpec: request.templateSpec,
              severity: request.severity,
              schedule: request.schedule,
              enabled: request.enabled,
            }, editRule.revision)
          : await reconClient.createRule(request)
      toast.success(
        isEdit
          ? "Rule updated"
          : isDuplicate
            ? "Rule duplicated"
            : "Rule created",
        { description: rule.name }
      )
      onSaved(rule)
      onOpenChange(false)
    } catch (error) {
      log.error("Rule save failed", { error })
      if (error instanceof ReconError) setServerError(error)
      else toast.error("Failed to save the rule")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[94vh] gap-0 overflow-hidden p-0 sm:max-w-5xl">
        <DialogHeader className="border-b px-5 py-4">
          <DialogTitle>
            {isEdit
              ? "Edit rule"
              : isDuplicate
                ? "Duplicate rule"
                : "New rule"}
          </DialogTitle>
          <DialogDescription>
            {TEMPLATE_META[draft.kind].blurb}
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-[calc(94vh-9rem)] space-y-5 overflow-y-auto px-4 py-5 sm:px-5">
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_14rem]">
            <Field label="Name">
              <Input
                value={draft.name}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    name: event.target.value,
                  }))
                }
                placeholder="e.g. Customer funds control"
              />
            </Field>
            <Field label="Severity">
              <Select
                value={draft.severity}
                onValueChange={(severity) =>
                  setDraft((current) => ({
                    ...current,
                    severity: severity as Severity,
                  }))
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SEVERITY_ORDER.map((severity) => (
                    <SelectItem key={severity} value={severity}>
                      {SEVERITY_META[severity].label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>

          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            {TEMPLATE_KINDS.filter(
              // Reconciliation v3.0.0 ships three templates (RFC 0022); the
              // others come in later phases. A rule already on one of them
              // keeps its card when edited or duplicated.
              (kind) => (V3_TEMPLATES as readonly string[]).includes(kind) || kind === draft.kind
            ).map((kind) => (
              <button
                key={kind}
                type="button"
                disabled={isEdit}
                onClick={() => changeKind(kind)}
                className={`rounded-md border p-3 text-left transition-colors ${
                  draft.kind === kind
                    ? "border-primary bg-primary/5"
                    : "hover:bg-muted/30"
                } disabled:cursor-default`}
              >
                <div className="text-sm font-medium">
                  {TEMPLATE_META[kind].label}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  {TEMPLATE_META[kind].blurb}
                </div>
              </button>
            ))}
          </div>

          <RunTimingFields
            periodType={draft.periodType}
            schedule={draft.schedule}
            periodTypeLocked={isEdit}
            onPeriodTypeChange={(periodType) =>
              setDraft((current) => ({ ...current, periodType }))
            }
            onScheduleChange={(schedule) =>
              setDraft((current) => ({ ...current, schedule }))
            }
          />

          <Card className="overflow-hidden">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-muted/30 px-3 py-2">
              <div>
                <div className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                  {draft.kind === "stale_holds"
                    ? "Hold set"
                    : draft.kind === "balance_bounds"
                      ? "Account set"
                      : "Named sources"}
                </div>
                <div className="text-[10px] text-muted-foreground">
                  {draft.kind === "stale_holds"
                    ? "The accounts holding funds — one per hold, never a shared pool, matched by address prefix."
                    : draft.kind === "balance_bounds"
                      ? "The accounts whose balance is bounded."
                      : "Labels are operator-facing; stable IDs carry every machine reference."}
                </div>
              </div>
              <div className="flex items-center gap-3">
                {supportsAllAssets(draft.kind) && (
                  <label className="flex items-center gap-2 text-xs">
                    <Switch
                      checked={allAssets(draft)}
                      onCheckedChange={(on) =>
                        setDraft((current) => setAllAssets(current, on))
                      }
                    />
                    <span>
                      Check every asset
                      <span className="ml-1 text-muted-foreground">
                        (one alert per asset)
                      </span>
                    </span>
                  </label>
                )}
                {!seed && (
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => changeKind(draft.kind)}
                  >
                    <Wand2 className="mr-1.5 h-3.5 w-3.5" /> Reset example
                  </Button>
                )}
              </div>
            </div>
            <div className="p-3">
              <NamedSourcesEditor
                sources={draft.sources}
                onChange={(sources) =>
                  setDraft((current) => replaceRuleSources(current, sources))
                }
                onIdChange={(index, _previous, next) =>
                  setDraft((current) =>
                    renameRuleSource(current, index, next)
                  )
                }
                ledgerOptions={ledgerOptions}
                fixedCount={
                  draft.kind === "exchange_rate_bounds" ||
                  draft.kind === "stale_holds" ||
                  draft.kind === "balance_bounds"
                }
                minSources={singleSource(draft.kind) ? 1 : 2}
                maxSources={singleSource(draft.kind) ? 1 : 32}
                backendError={backendError}
              />
            </div>
          </Card>

          <TemplateEditor
            draft={draft}
            onChange={setDraft}
            backendError={backendError}
          />

          <div className="flex items-center justify-between rounded-lg border px-3 py-2">
            <div>
              <Label>Enabled</Label>
              <p className="text-xs text-muted-foreground">
                Start monitoring this rule as soon as it is saved.
              </p>
            </div>
            <Switch
              checked={draft.enabled}
              onCheckedChange={(enabled) =>
                setDraft((current) => ({ ...current, enabled }))
              }
            />
          </div>

          {(issues.length > 0 || serverError) && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive-foreground">
              <div className="flex items-center gap-2 font-medium">
                <AlertTriangle className="h-4 w-4" /> Review the rule definition
              </div>
              <ul className="mt-1 list-disc space-y-0.5 pl-5 text-xs">
                {issues.map((issue) => (
                  <li key={`${issue.path}:${issue.message}`}>
                    {issue.message}{" "}
                    <span className="font-mono">({issue.path})</span>
                  </li>
                ))}
                {serverError && (
                  <li>
                    <span className="font-medium">
                      {serverError.errorCode}:
                    </span>{" "}
                    {serverError.isConflict
                      ? "Someone changed this rule since you opened it. Close this dialog, reopen the rule to load its latest version, then apply your edit again."
                      : backendError}
                  </li>
                )}
              </ul>
            </div>
          )}
          {preview && (
            <PreviewResult preview={preview.result} stale={previewStale} />
          )}
        </div>
        <DialogFooter className="flex-row flex-wrap items-center gap-2 border-t px-5 py-4">
          <Button
            variant="outline"
            className="mr-auto"
            onClick={runPreview}
            disabled={issues.length > 0 || previewing || submitting}
            title="Evaluate this configuration on the current balances. Nothing is saved and no alert is opened."
          >
            {previewing ? (
              <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />
            ) : (
              <FlaskConical className="mr-1.5 h-4 w-4" />
            )}
            Test on current balances
          </Button>
          <Button
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            Cancel
          </Button>
          <Button onClick={submit} disabled={issues.length > 0 || submitting}>
            {submitting && <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />}
            {isEdit ? "Save changes" : "Create rule"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function TemplateEditor({
  draft,
  onChange,
  backendError,
}: {
  draft: RuleFormDraft
  onChange: (draft: RuleFormDraft) => void
  backendError?: string
}) {
  switch (draft.kind) {
    case "balance_equation":
      return (
        <BalanceEquationEditor
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "exchange_rate_bounds":
      return (
        <ExchangeRateBoundsEditor
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "source_consensus":
      return (
        <SourceConsensusEditor
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "coverage_ratio_bounds":
      return (
        <CoverageRatioBoundsEditor
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "balance_bounds":
      return (
        <BalanceBoundsEditor
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "stale_holds":
      return (
        <StaleHoldsEditor
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
  }
}

function Field({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 space-y-1.5">
      <Label className="text-xs">{label}</Label>
      {children}
    </div>
  )
}

const PREVIEW_TONE: Record<RulePreview["result"], string> = {
  PASS: "border-green-foreground/25 bg-green-background/50",
  FAIL: "border-destructive/40 bg-destructive/5",
  ERROR: "border-warning/40 bg-warning/5",
}

const PREVIEW_TITLE: Record<RulePreview["result"], string> = {
  PASS: "Passes on the current balances",
  FAIL: "Fails on the current balances",
  ERROR: "Could not be evaluated",
}

/** The verdict of a dry run, with its evidence. Nothing of it was saved. */
function PreviewResult({ preview, stale }: { preview: RulePreview; stale: boolean }) {
  const ref = useRef<HTMLElement>(null)
  // The form is long: bring each new result into view.
  useEffect(() => {
    ref.current?.scrollIntoView({ behavior: "smooth", block: "nearest" })
  }, [preview])
  const outcomes = captureEvidenceOutcomes(preview.evidence)
  const gap = evidenceGap(preview.evidence)
  const at = new Date(preview.pit).toLocaleTimeString()
  return (
    <section
      ref={ref}
      aria-live="polite"
      className={`space-y-3 rounded-md border p-3 ${PREVIEW_TONE[preview.result]} ${stale ? "opacity-60" : ""}`}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-sm font-semibold">{PREVIEW_TITLE[preview.result]}</h3>
        <span className="text-xs text-muted-foreground">
          Test at {at} · not saved, no alert opened
        </span>
      </div>
      {stale && (
        <p className="text-xs text-warning-foreground">
          The configuration changed since this test. Test again to see the new result.
        </p>
      )}
      {gap && <p className="text-sm">{gap}</p>}
      {preview.result === "ERROR" && preview.error && (
        <p className="break-words font-mono text-xs text-muted-foreground">{preview.error}</p>
      )}
      {outcomes.length > 0 && (
        <div className="grid gap-2">
          {outcomes.map((outcome) => (
            <div key={outcome.fingerprint} className="min-w-0 rounded-md bg-background/70 p-2">
              <div className="mb-1 font-mono text-[11px] text-muted-foreground">
                {outcome.fingerprint}
              </div>
              {isEvidence(outcome.evidence) ? (
                <EvidenceView evidence={outcome.evidence} passed={outcome.passed} compact />
              ) : (
                <span className="text-xs">{outcome.passed ? "passes" : "fails"}</span>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
