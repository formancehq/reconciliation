"use client"

import { AlertTriangle } from "lucide-react"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  backendFieldPath,
  coefficientPath,
  type NamedSourceV2,
  type PortfolioSideV2,
  type RateBoundsDraftV2,
  type RuleFormDraftV2,
} from "@/lib/recon"

export function BalanceEquationEditorV2({
  draft,
  onChange,
  backendError,
}: TemplateEditorProps) {
  return (
    <Card className="space-y-3 p-3">
      <div>
        <div className="text-sm font-medium">Signed equation = 0</div>
        <p className="text-xs text-muted-foreground">
          Every named source participates exactly once in the signed sum.
        </p>
      </div>
      <SignedSourceTermsEditorV2
        draft={draft}
        onChange={onChange}
        backendError={backendError}
      />
      <div className="max-w-xs">
        <NonNegativeIntegerToleranceEditorV2
          value={draft.tolerance}
          onChange={(tolerance) => onChange({ ...draft, tolerance })}
          backendError={backendError}
        />
      </div>
    </Card>
  )
}

export function ExchangeRateBoundsEditorV2({
  draft,
  onChange,
  backendError,
}: TemplateEditorProps) {
  return (
    <Card className="space-y-3 p-3">
      <div>
        <div className="text-sm font-medium">Base and quote roles</div>
        <p className="text-xs text-muted-foreground">
          The observed rate is quote major units per one base major unit.
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <SourceSelectV2
          label="Base source"
          path="baseSource"
          value={draft.baseSource}
          onChange={(baseSource) => onChange({ ...draft, baseSource })}
          sources={draft.sources}
          backendError={backendError}
        />
        <SourceSelectV2
          label="Quote source"
          path="quoteSource"
          value={draft.quoteSource}
          onChange={(quoteSource) => onChange({ ...draft, quoteSource })}
          sources={draft.sources}
          backendError={backendError}
        />
      </div>
      <ExactRateBoundsEditorV2
        value={draft.rate}
        onChange={(rate) => onChange({ ...draft, rate })}
        pathPrefix="rate"
        noun="rate"
        backendError={backendError}
      />
    </Card>
  )
}

export function SourceConsensusEditorV2({
  draft,
  onChange,
  backendError,
}: TemplateEditorProps) {
  return (
    <Card className="space-y-3 p-3">
      <div>
        <div className="text-sm font-medium">
          widest balance spread ≤ tolerance
        </div>
        <p className="text-xs text-muted-foreground">
          Every declared source is required. The rule checks maximum minus
          minimum across all sources; there is no privileged reference,
          quorum, or ignore-missing mode.
        </p>
      </div>
      <div className="max-w-xs">
        <NonNegativeIntegerToleranceEditorV2
          value={draft.tolerance}
          onChange={(tolerance) => onChange({ ...draft, tolerance })}
          backendError={backendError}
        />
      </div>
    </Card>
  )
}

export function CoverageRatioBoundsEditorV2({
  draft,
  onChange,
  backendError,
}: TemplateEditorProps) {
  return (
    <Card className="space-y-3 p-3">
      <div>
        <div className="text-sm font-medium">Coverage portfolios</div>
        <p className="text-xs text-muted-foreground">
          Assign every named source exactly once to a signed numerator or
          denominator portfolio.
        </p>
      </div>
      <SignedSourceTermsEditorV2
        draft={draft}
        onChange={onChange}
        backendError={backendError}
        withPortfolio
      />
      <div className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-900 dark:text-amber-200">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
        <span>
          If the signed denominator portfolio totals zero, evaluation fails
          with an undefined ratio; it is not treated as an engine error.
        </span>
      </div>
      <ExactRateBoundsEditorV2
        value={draft.rate}
        onChange={(rate) => onChange({ ...draft, rate })}
        pathPrefix="ratio"
        noun="ratio"
        backendError={backendError}
      />
    </Card>
  )
}

export function SignedSourceTermsEditorV2({
  draft,
  onChange,
  backendError,
  withPortfolio = false,
}: TemplateEditorProps & { withPortfolio?: boolean }) {
  return (
    <div className="space-y-2">
      {draft.sources.map((source, index) => {
        const coefficientError = backendFieldPath(
          backendError,
          coefficientPath(draft, source.id, index)
        )
        const side = draft.portfolios[source.id] ?? "numerator"
        const sideSources = draft.sources.filter(
          (candidate) =>
            (draft.portfolios[candidate.id] ?? "numerator") === side
        )
        const termIndex = sideSources.findIndex(
          (candidate) => candidate.id === source.id
        )
        const sourceError = withPortfolio
          ? backendFieldPath(
              backendError,
              `${side}Terms[${termIndex}].source`
            )
          : backendFieldPath(backendError, `terms[${index}].source`)
        return (
          <div
            key={source.id}
            className={`grid gap-2 rounded-md border p-2 ${
              withPortfolio
                ? "sm:grid-cols-[minmax(0,1fr)_10rem_10rem]"
                : "sm:grid-cols-[minmax(0,1fr)_12rem]"
            }`}
          >
            <div className="min-w-0 self-center">
              <div className="truncate text-sm font-medium">
                {source.label || source.id}
              </div>
              <div className="truncate font-mono text-[10px] text-muted-foreground">
                {source.id}
              </div>
              {sourceError && <FieldErrorV2 error={sourceError} />}
            </div>
            {withPortfolio && (
              <Select
                value={side}
                onValueChange={(value) =>
                  onChange({
                    ...draft,
                    portfolios: {
                      ...draft.portfolios,
                      [source.id]: value as PortfolioSideV2,
                    },
                  })
                }
              >
                <SelectTrigger aria-label={`${source.id} portfolio`}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="numerator">Numerator</SelectItem>
                  <SelectItem value="denominator">Denominator</SelectItem>
                </SelectContent>
              </Select>
            )}
            <div>
              <Input
                aria-label={`${source.id} coefficient`}
                value={draft.coefficients[source.id] ?? "1"}
                onChange={(event) =>
                  onChange({
                    ...draft,
                    coefficients: {
                      ...draft.coefficients,
                      [source.id]: event.target.value,
                    },
                  })
                }
                inputMode="numeric"
                className="font-mono"
              />
              {coefficientError && <FieldErrorV2 error={coefficientError} />}
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function NonNegativeIntegerToleranceEditorV2({
  value,
  onChange,
  backendError,
}: {
  value: string
  onChange: (value: string) => void
  backendError?: string
}) {
  return (
    <FieldV2
      label="Tolerance (minor units)"
      error={backendFieldPath(backendError, "tolerance")}
    >
      <Input
        value={value}
        onChange={(event) => onChange(event.target.value)}
        inputMode="numeric"
        className="font-mono"
      />
    </FieldV2>
  )
}

export function ExactRateBoundsEditorV2({
  value,
  onChange,
  pathPrefix,
  noun,
  backendError,
}: {
  value: RateBoundsDraftV2
  onChange: (value: RateBoundsDraftV2) => void
  pathPrefix: "rate" | "ratio"
  noun: "rate" | "ratio"
  backendError?: string
}) {
  const update = (patch: Partial<RateBoundsDraftV2>) =>
    onChange({ ...value, ...patch })
  return (
    <div className="space-y-3 border-t pt-3">
      <FieldV2 label="Bounds mode">
        <Select
          value={value.mode}
          onValueChange={(mode) =>
            update({ mode: mode as RateBoundsDraftV2["mode"] })
          }
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="explicit">
              Explicit inclusive min / max
            </SelectItem>
            <SelectItem value="target">
              Target plus basis-point tolerance
            </SelectItem>
          </SelectContent>
        </Select>
      </FieldV2>
      {value.mode === "explicit" ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <FieldV2
            label="Inclusive minimum"
            error={backendFieldPath(backendError, `${pathPrefix}.min`)}
          >
            <Input
              value={value.min}
              onChange={(event) => update({ min: event.target.value })}
              inputMode="decimal"
              className="font-mono"
            />
          </FieldV2>
          <FieldV2
            label="Inclusive maximum"
            error={backendFieldPath(backendError, `${pathPrefix}.max`)}
          >
            <Input
              value={value.max}
              onChange={(event) => update({ max: event.target.value })}
              inputMode="decimal"
              className="font-mono"
            />
          </FieldV2>
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          <FieldV2
            label="Target"
            error={backendFieldPath(backendError, `${pathPrefix}.target`)}
          >
            <Input
              value={value.target}
              onChange={(event) => update({ target: event.target.value })}
              inputMode="decimal"
              className="font-mono"
            />
          </FieldV2>
          <FieldV2
            label="Tolerance (basis points)"
            error={backendFieldPath(
              backendError,
              `${pathPrefix}.toleranceBps`
            )}
          >
            <Input
              value={value.toleranceBps}
              onChange={(event) => update({ toleranceBps: event.target.value })}
              inputMode="numeric"
              className="font-mono"
            />
          </FieldV2>
        </div>
      )}
      <p className="text-[10px] text-muted-foreground">
        The {noun} decimals stay as exact strings; validation does not use
        binary floating point.
      </p>
    </div>
  )
}

interface TemplateEditorProps {
  draft: RuleFormDraftV2
  onChange: (draft: RuleFormDraftV2) => void
  backendError?: string
}

function SourceSelectV2({
  label,
  path,
  value,
  onChange,
  sources,
  backendError,
}: {
  label: string
  path: string
  value: string
  onChange: (value: string) => void
  sources: NamedSourceV2[]
  backendError?: string
}) {
  return (
    <FieldV2 label={label} error={backendFieldPath(backendError, path)}>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {sources.map((source) => (
            <SelectItem key={source.id} value={source.id}>
              {source.label || source.id} · {source.asset}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </FieldV2>
  )
}

function FieldV2({
  label,
  error,
  children,
}: {
  label: string
  error?: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 space-y-1.5">
      <Label className="text-xs">{label}</Label>
      {children}
      {error && <FieldErrorV2 error={error} />}
    </div>
  )
}

function FieldErrorV2({ error }: { error: string }) {
  return (
    <p className="mt-1 font-mono text-xs break-words text-destructive">
      {error}
    </p>
  )
}
