"use client"

import { useEffect, useMemo, useState } from "react"
import { AlertTriangle, Loader2, Wand2 } from "lucide-react"
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
  backendValidationText,
  changeRuleTemplateV2,
  createRuleFormDraftV2,
  ReconError,
  reconClientV2,
  renameRuleSourceV2,
  replaceRuleSourcesV2,
  SEVERITY_META,
  SEVERITY_ORDER,
  serializeRuleFormV2,
  TEMPLATE_KINDS_V2,
  TEMPLATE_META_V2,
  validateRuleFormV2,
  type RuleFormDraftV2,
  type RuleV2,
  type Severity,
  type TemplateKindV2,
} from "@/lib/recon"
import { useChartStore } from "@/stores/chartStore"
import { NamedSourcesEditor } from "../NamedSourcesEditor"
import {
  BalanceEquationEditorV2,
  CoverageRatioBoundsEditorV2,
  ExchangeRateBoundsEditorV2,
  SourceConsensusEditorV2,
} from "../v2/V2TemplateEditors"
import { RunTimingFields } from "./CreateRuleDialog"

const log = createLogger("Recon")

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  editRule?: RuleV2 | null
  duplicateRule?: RuleV2 | null
  onSaved: (rule: RuleV2) => void
}

export function CreateRuleDialogV2({
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
    <CreateRuleDialogV2Open
      key={modeKey}
      {...props}
      open
      editRule={editRule}
      duplicateRule={duplicateRule}
    />
  )
}

function CreateRuleDialogV2Open({
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
  const [draft, setDraft] = useState<RuleFormDraftV2>(() =>
    createRuleFormDraftV2({
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

  const issues = useMemo(() => validateRuleFormV2(draft), [draft])
  const backendError = backendValidationText(serverError)

  const changeKind = (kind: TemplateKindV2) => {
    setDraft((current) => changeRuleTemplateV2(current, kind, activeLedger))
    setServerError(null)
  }

  const submit = async () => {
    setSubmitting(true)
    setServerError(null)
    try {
      const request = serializeRuleFormV2(draft)
      const rule =
        isEdit && editRule
          ? await reconClientV2.patchRule(editRule.id, {
              name: request.name,
              templateKind: request.templateKind,
              templateSpec: request.templateSpec,
              severity: request.severity,
              schedule: request.schedule,
              enabled: request.enabled,
            })
          : await reconClientV2.createRule(request)
      toast.success(
        isEdit
          ? "V2 rule updated"
          : isDuplicate
            ? "V2 rule duplicated"
            : "V2 rule created",
        { description: rule.name }
      )
      onSaved(rule)
      onOpenChange(false)
    } catch (error) {
      log.error("V2 rule save failed", { error })
      if (error instanceof ReconError) setServerError(error)
      else toast.error("Failed to save V2 rule")
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
              ? "Edit named-source rule"
              : isDuplicate
                ? "Duplicate named-source rule"
                : "New named-source rule"}
          </DialogTitle>
          <DialogDescription>
            {TEMPLATE_META_V2[draft.kind].blurb}
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-[calc(94vh-9rem)] space-y-5 overflow-y-auto px-4 py-5 sm:px-5">
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_14rem]">
            <FieldV2 label="Name">
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
            </FieldV2>
            <FieldV2 label="Severity">
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
            </FieldV2>
          </div>

          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            {TEMPLATE_KINDS_V2.map((kind) => (
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
                  {TEMPLATE_META_V2[kind].label}
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  {TEMPLATE_META_V2[kind].blurb}
                </div>
              </button>
            ))}
          </div>

          <RunTimingFields
            cadence={draft.cadence}
            schedule={draft.schedule}
            cadenceLocked={isEdit}
            onCadenceChange={(cadence) =>
              setDraft((current) => ({ ...current, cadence }))
            }
            onScheduleChange={(schedule) =>
              setDraft((current) => ({ ...current, schedule }))
            }
          />

          <Card className="overflow-hidden">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b bg-muted/30 px-3 py-2">
              <div>
                <div className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">
                  Named sources
                </div>
                <div className="text-[10px] text-muted-foreground">
                  Labels are operator-facing; stable IDs carry every machine
                  reference.
                </div>
              </div>
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
            <div className="p-3">
              <NamedSourcesEditor
                sources={draft.sources}
                onChange={(sources) =>
                  setDraft((current) => replaceRuleSourcesV2(current, sources))
                }
                onIdChange={(index, _previous, next) =>
                  setDraft((current) =>
                    renameRuleSourceV2(current, index, next)
                  )
                }
                ledgerOptions={ledgerOptions}
                fixedCount={draft.kind === "exchange_rate_bounds"}
                backendError={backendError}
              />
            </div>
          </Card>

          <TemplateEditorV2
            draft={draft}
            onChange={setDraft}
            backendError={backendError}
          />

          <div className="flex items-center justify-between rounded-lg border px-3 py-2">
            <div>
              <Label>Enabled</Label>
              <p className="text-xs text-muted-foreground">
                Start monitoring this V2 contract after save.
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
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
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
                    {backendError}
                  </li>
                )}
              </ul>
            </div>
          )}
        </div>
        <DialogFooter className="border-t px-5 py-4">
          <Button
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            Cancel
          </Button>
          <Button onClick={submit} disabled={issues.length > 0 || submitting}>
            {submitting && <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />}
            {isEdit ? "Save changes" : "Create V2 rule"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function TemplateEditorV2({
  draft,
  onChange,
  backendError,
}: {
  draft: RuleFormDraftV2
  onChange: (draft: RuleFormDraftV2) => void
  backendError?: string
}) {
  switch (draft.kind) {
    case "balance_equation":
      return (
        <BalanceEquationEditorV2
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "exchange_rate_bounds":
      return (
        <ExchangeRateBoundsEditorV2
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "source_consensus":
      return (
        <SourceConsensusEditorV2
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
    case "coverage_ratio_bounds":
      return (
        <CoverageRatioBoundsEditorV2
          draft={draft}
          onChange={onChange}
          backendError={backendError}
        />
      )
  }
}

function FieldV2({
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
