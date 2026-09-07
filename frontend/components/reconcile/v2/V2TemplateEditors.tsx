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
  ASSET_WILDCARD_V2,
  MAX_IDENTITY_KEYS_V2,
  type BoundDraftV2,
  type InstantEncodingV2,
  type NamedSourceV2,
  type PortfolioSideV2,
  type RateBoundsDraftV2,
  type RuleFormDraftV2,
  type StaleHoldsModeV2,
  type StaleHoldsScopeV2,
} from "@/lib/recon"
import { useLedgerMetaFields } from "../useLedgerMetaFields"

/** Radix selects cannot hold an empty value; this stands in for "not set". */
const NO_KEY = "__none__"

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

export function BalanceBoundsEditorV2({
  draft,
  onChange,
  backendError,
}: TemplateEditorProps) {
  const declared = draft.sources[0]?.asset ?? ""
  const wildcard = declared === ASSET_WILDCARD_V2
  const setBounds = (bounds: BoundDraftV2[]) => onChange({ ...draft, bounds })
  const update = (index: number, patch: Partial<BoundDraftV2>) =>
    setBounds(draft.bounds.map((b, i) => (i === index ? { ...b, ...patch } : b)))

  return (
    <Card className="space-y-3 p-3">
      <div>
        <div className="text-sm font-medium">Balance within limits, per asset</div>
        <p className="text-xs text-muted-foreground">
          The assets you list here are exactly the assets checked — not the ones
          the account set happens to hold. That is deliberate: a minimum on a set
          that has drained to nothing still has to fail, so an asset you name but
          the set does not hold is checked as zero.
        </p>
      </div>

      <div className="space-y-2">
        {draft.bounds.map((bound, index) => (
          <div key={index} className="flex flex-wrap items-end gap-2">
            <div className="w-28">
              <FieldV2
                label={index === 0 ? "Asset" : ""}
                error={backendFieldPath(backendError, `bounds[${index}].asset`)}
              >
                <Input
                  value={bound.asset}
                  onChange={(event) => update(index, { asset: event.target.value })}
                  placeholder="USD/2"
                  className="font-mono"
                  disabled={!wildcard && draft.bounds.length === 1}
                />
              </FieldV2>
            </div>
            <div className="min-w-28 flex-1">
              <FieldV2
                label={index === 0 ? "Minimum" : ""}
                error={backendFieldPath(backendError, `bounds[${index}].min`)}
              >
                <Input
                  value={bound.min}
                  onChange={(event) => update(index, { min: event.target.value })}
                  placeholder="no minimum"
                  inputMode="numeric"
                  className="font-mono"
                />
              </FieldV2>
            </div>
            <div className="min-w-28 flex-1">
              <FieldV2
                label={index === 0 ? "Maximum" : ""}
                error={backendFieldPath(backendError, `bounds[${index}].max`)}
              >
                <Input
                  value={bound.max}
                  onChange={(event) => update(index, { max: event.target.value })}
                  placeholder="no maximum"
                  inputMode="numeric"
                  className="font-mono"
                />
              </FieldV2>
            </div>
            <button
              type="button"
              className="mb-1.5 text-xs text-muted-foreground hover:text-destructive disabled:opacity-40"
              onClick={() => setBounds(draft.bounds.filter((_, i) => i !== index))}
              disabled={draft.bounds.length === 1}
            >
              Remove
            </button>
          </div>
        ))}
      </div>

      {wildcard ? (
        <button
          type="button"
          className="text-xs font-medium text-muted-foreground hover:text-foreground"
          onClick={() => setBounds([...draft.bounds, { asset: "", min: "", max: "" }])}
        >
          + Bound another asset
        </button>
      ) : (
        <p className="text-xs text-muted-foreground">
          The source declares {declared || "one asset"}, so it is bounded in that
          asset alone. Switch it to <span className="font-medium">every asset</span>{" "}
          to bound several.
        </p>
      )}

      <p className="text-xs text-muted-foreground">
        Amounts are whole numbers of minor units and may be negative — a limit on
        a liability set usually is. Leave a side empty for unbounded; leaving both
        empty is a rule that can never fail.
      </p>
    </Card>
  )
}

export function StaleHoldsEditorV2({
  draft,
  onChange,
  backendError,
}: TemplateEditorProps) {
  const approaching = draft.mode === "approaching"
  const setDeadline = (patch: Partial<RuleFormDraftV2["deadline"]>) =>
    onChange({ ...draft, deadline: { ...draft.deadline, ...patch } })

  return (
    <Card className="space-y-4 p-3">
      <div>
        <div className="text-sm font-medium">
          {approaching
            ? "Holds approaching their deadline"
            : "Holds past their deadline"}
        </div>
        <p className="text-xs text-muted-foreground">
          A hold is one account still holding funds — normally one per
          authorisation. Its deadline is read from the account&apos;s own
          metadata; released holds, whose balance is back to zero, are ignored.
        </p>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <MetadataKeySelectV2
          label="Issuer expiry key"
          ledger={draft.sources[0]?.ledger ?? ""}
          value={draft.deadline.expiryKey}
          onChange={(expiryKey) => setDeadline({ expiryKey })}
          error={backendFieldPath(backendError, "deadline.expiryKey")}
          hint="The date the issuer says the hold expires. Must be an indexed datetime or integer key."
        />
        <FieldV2
          label="Value format"
          error={backendFieldPath(backendError, "deadline.encoding")}
        >
          <Select
            value={draft.deadline.encoding}
            onValueChange={(encoding) =>
              setDeadline({ encoding: encoding as InstantEncodingV2 })
            }
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="datetime">Datetime</SelectItem>
              <SelectItem value="epoch_seconds">Epoch seconds</SelectItem>
              <SelectItem value="epoch_millis">Epoch milliseconds</SelectItem>
              <SelectItem value="epoch_micros">Epoch microseconds</SelectItem>
            </SelectContent>
          </Select>
        </FieldV2>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <MetadataKeySelectV2
          label="Placed-at key (fallback)"
          ledger={draft.sources[0]?.ledger ?? ""}
          value={draft.deadline.createdKey}
          onChange={(createdKey) => setDeadline({ createdKey })}
          error={backendFieldPath(backendError, "deadline.createdKey")}
          hint="Used for holds with no issuer expiry: this date plus the maximum age below."
        />
        <FieldV2
          label="Maximum age"
          error={backendFieldPath(backendError, "deadline.maxAge")}
        >
          <Input
            value={draft.deadline.maxAge}
            onChange={(event) => setDeadline({ maxAge: event.target.value })}
            placeholder="48h"
            className="font-mono"
            disabled={!draft.deadline.createdKey.trim()}
          />
          <p className="mt-1 text-xs text-muted-foreground">
            How long a hold may live once placed. Required with a placed-at key.
          </p>
        </FieldV2>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <FieldV2 label="Flag holds that are">
          <Select
            value={draft.mode}
            onValueChange={(mode) =>
              onChange({
                ...draft,
                mode: mode as StaleHoldsModeV2,
                // warnWithin belongs to the approaching band only; the server
                // rejects it in stale mode, so clear it rather than hide it.
                warnWithin: mode === "approaching" ? draft.warnWithin : "",
              })
            }
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="stale">Already past the deadline</SelectItem>
              <SelectItem value="approaching">
                Approaching the deadline
              </SelectItem>
            </SelectContent>
          </Select>
        </FieldV2>
        {approaching && (
          <FieldV2
            label="Warning window"
            error={backendFieldPath(backendError, "warnWithin")}
          >
            <Input
              value={draft.warnWithin}
              onChange={(event) =>
                onChange({ ...draft, warnWithin: event.target.value })
              }
              placeholder="6h"
              className="font-mono"
            />
            <p className="mt-1 text-xs text-muted-foreground">
              Set this wider than the rule&apos;s run interval, or a hold can
              cross the window between two runs without ever warning.
            </p>
          </FieldV2>
        )}
      </div>

      {approaching && (
        <p className="text-xs text-muted-foreground">
          This is a band, not a threshold: once a hold goes past its deadline it
          leaves this rule. Pair it with a second rule set to{" "}
          <span className="font-medium">already past the deadline</span> at a
          higher severity — the early warning resolves itself as the breach
          alert opens.
        </p>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        <FieldV2 label="Alert granularity">
          <Select
            value={draft.scope}
            onValueChange={(scope) =>
              onChange({ ...draft, scope: scope as StaleHoldsScopeV2 })
            }
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="per_hold">One alert per hold</SelectItem>
              <SelectItem value="aggregate">
                One alert per asset, with totals
              </SelectItem>
            </SelectContent>
          </Select>
          <p className="mt-1 text-xs text-muted-foreground">
            Per hold names the stuck authorisation and resolves itself when the
            hold clears. Choose totals when the stale set can get large.
          </p>
        </FieldV2>
        <FieldV2
          label="Hold limit per run"
          error={backendFieldPath(backendError, "maxHoldsScanned")}
        >
          <Input
            value={draft.maxHoldsScanned}
            onChange={(event) =>
              onChange({ ...draft, maxHoldsScanned: event.target.value })
            }
            placeholder={draft.scope === "per_hold" ? "1000" : "50000"}
            inputMode="numeric"
            className="font-mono"
          />
          <p className="mt-1 text-xs text-muted-foreground">
            Past this the run stops and says so rather than filling the inbox.
            Leave empty for the default.
          </p>
        </FieldV2>
      </div>

      <FieldV2
        label="Label the alert with (optional)"
        error={backendFieldPath(backendError, "identityKeys")}
      >
        <Input
          value={draft.identityKeys.join(", ")}
          onChange={(event) =>
            onChange({
              ...draft,
              identityKeys: event.target.value
                .split(",")
                .map((key) => key.trim())
                .filter(Boolean),
            })
          }
          placeholder="enfuce_auth_id, card_id"
          className="font-mono"
        />
        <p className="mt-1 text-xs text-muted-foreground">
          Up to {MAX_IDENTITY_KEYS_V2} metadata keys, copied onto each alert so
          it names the authorisation rather than a ledger address. These are
          labels, not filters — they need no index. Alert evidence is durable
          and widely readable, so leave cardholder details out.
        </p>
      </FieldV2>
    </Card>
  )
}

/**
 * A deadline key must be range-filtered by the ledger, so only indexed datetime
 * and integer keys are offered. When the ledger's keys cannot be listed, fall
 * back to free text rather than blocking the rule.
 */
function MetadataKeySelectV2({
  label,
  ledger,
  value,
  onChange,
  error,
  hint,
}: {
  label: string
  ledger: string
  value: string
  onChange: (value: string) => void
  error?: string
  hint: string
}) {
  const { fields } = useLedgerMetaFields(ledger)
  const options = fields.filter(
    (field) =>
      field.ready &&
      (field.kind === "datetime" ||
        field.kind === "int" ||
        field.kind === "uint")
  )

  return (
    <FieldV2 label={label} error={error}>
      {options.length === 0 ? (
        <Input
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder="hold_expires_at"
          className="font-mono"
        />
      ) : (
        <Select
          value={value || NO_KEY}
          onValueChange={(next) => onChange(next === NO_KEY ? "" : next)}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_KEY}>Not set</SelectItem>
            {options.map((field) => (
              <SelectItem key={field.key} value={field.key}>
                {field.key} · {field.kind}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      <p className="mt-1 text-xs text-muted-foreground">{hint}</p>
    </FieldV2>
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
