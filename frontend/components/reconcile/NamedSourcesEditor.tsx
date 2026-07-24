"use client"

import { useState } from "react"
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
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
import type { NamedSourceV2 } from "@/lib/recon"
import {
  addNamedSourceV2,
  backendFieldPath,
  backendSourcePath,
  effectiveSourceKind,
  emptyNamedSourceV2,
  moveNamedSourceV2,
  removeNamedSourceV2,
  sourceName,
} from "@/lib/recon"
import { AccountSelectorInput } from "./AccountSelectorInput"
import { LedgerCombobox } from "./LedgerCombobox"
import { MetaFilterBuilder } from "./MetaFilterBuilder"
import { compileReconQuery, parseReconQuery } from "@/lib/recon/querySpec"

export const emptyNamedSource = emptyNamedSourceV2

export function NamedSourcesEditor({
  sources,
  onChange,
  onIdChange,
  ledgerOptions,
  minSources = 2,
  maxSources = 32,
  fixedCount = false,
  backendError,
}: {
  sources: NamedSourceV2[]
  onChange: (sources: NamedSourceV2[]) => void
  onIdChange?: (index: number, previous: string, next: string) => void
  ledgerOptions: string[]
  minSources?: number
  maxSources?: number
  fixedCount?: boolean
  backendError?: string
}) {
  const [idErrors, setIdErrors] = useState<Record<number, string>>({})
  const update = (index: number, source: NamedSourceV2) =>
    onChange(
      sources.map((item, itemIndex) => (itemIndex === index ? source : item))
    )
  const move = (from: number, to: number) =>
    onChange(moveNamedSourceV2(sources, from, to))

  return (
    <div className="space-y-3">
      {sources.map((source, index) => {
        const query = parseReconQuery(source.query)
        const kind = effectiveSourceKind(source)
        const sourceError = backendSourcePath(backendError, index)
        const fieldError = (field: string) =>
          backendFieldPath(backendError, `sources[${index}].${field}`)
        const hasPlacedError = [
          "id",
          "label",
          "kind",
          "ledger",
          "asset",
          "metadataKey",
          "query",
        ].some((field) => fieldError(field))
        const setQuery = (patch: Partial<typeof query>) => {
          const next = { ...query, ...patch }
          update(index, {
            ...source,
            query: compileReconQuery(next.address, next.meta, next.metaComb),
          })
        }
        return (
          <Card key={index} className="min-w-0 overflow-hidden">
            <div className="flex flex-wrap items-center gap-2 border-b bg-muted/30 px-3 py-2">
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">
                  {sourceName(source) || `Source ${index + 1}`}
                </div>
                <div className="truncate font-mono text-[11px] text-muted-foreground">
                  id: {source.id || "required"}
                </div>
              </div>
              {!fixedCount && (
                <div className="flex items-center gap-1">
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    disabled={index === 0}
                    onClick={() => move(index, index - 1)}
                    aria-label={`Move ${sourceName(source)} up`}
                  >
                    <ArrowUp className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    disabled={index === sources.length - 1}
                    onClick={() => move(index, index + 1)}
                    aria-label={`Move ${sourceName(source)} down`}
                  >
                    <ArrowDown className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    type="button"
                    size="icon-sm"
                    variant="ghost"
                    disabled={sources.length <= minSources}
                    onClick={() =>
                      onChange(removeNamedSourceV2(sources, index))
                    }
                    aria-label={`Remove ${sourceName(source)}`}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              )}
            </div>
            <div className="grid min-w-0 gap-3 p-3 sm:grid-cols-2">
              <Field
                label="Stable source ID"
                hint="Used by terms and evidence"
                error={idErrors[index] ?? fieldError("id")}
              >
                <Input
                  value={source.id}
                  onChange={(event) => {
                    const previous = source.id
                    const next = event.target.value
                    if (
                      next !== previous &&
                      sources.some(
                        (candidate, candidateIndex) =>
                          candidateIndex !== index && candidate.id === next
                      )
                    ) {
                      setIdErrors((current) => ({
                        ...current,
                        [index]: `Source ID ${next} is already in use.`,
                      }))
                      return
                    }
                    setIdErrors((current) => {
                      const updated = { ...current }
                      delete updated[index]
                      return updated
                    })
                    if (onIdChange) onIdChange(index, previous, next)
                    else update(index, { ...source, id: next })
                  }}
                  placeholder="customerBook"
                  className="font-mono"
                />
              </Field>
              <Field
                label="Operator label"
                hint="Optional; shown instead of the ID"
                error={fieldError("label")}
              >
                <Input
                  value={source.label ?? ""}
                  onChange={(event) =>
                    update(index, {
                      ...source,
                      label: event.target.value || undefined,
                    })
                  }
                  placeholder="Customer book"
                />
              </Field>
              <Field label="Source kind" error={fieldError("kind")}>
                <Select
                  value={kind}
                  onValueChange={(value) => {
                    if (value === "account_metadata") {
                      update(index, {
                        ...source,
                        kind: "account_metadata",
                        metadataKey: "reported_balance",
                      })
                    } else {
                      update(index, {
                        id: source.id,
                        label: source.label,
                        kind: "ledger",
                        ledger: source.ledger,
                        query: source.query,
                        asset: source.asset,
                      })
                    }
                  }}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ledger">Ledger balance</SelectItem>
                    <SelectItem value="account_metadata">
                      Account metadata
                    </SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Ledger" error={fieldError("ledger")}>
                <LedgerCombobox
                  value={source.ledger}
                  onChange={(ledger) => update(index, { ...source, ledger })}
                  options={ledgerOptions}
                />
              </Field>
              <Field
                label="Declared asset"
                hint="Explicit and independent from metadata key"
                error={fieldError("asset")}
              >
                <Input
                  value={source.asset}
                  onChange={(event) =>
                    update(index, { ...source, asset: event.target.value })
                  }
                  placeholder="USD/2"
                  className="font-mono"
                />
              </Field>
              {kind === "account_metadata" && (
                <Field
                  label="Metadata key"
                  hint="Read as an integer in the declared asset"
                  error={fieldError("metadataKey")}
                >
                  <Input
                    value={
                      source.kind === "account_metadata"
                        ? source.metadataKey
                        : ""
                    }
                    onChange={(event) =>
                      update(index, {
                        ...source,
                        kind: "account_metadata",
                        metadataKey: event.target.value,
                      } as NamedSourceV2)
                    }
                    placeholder="reported_balance"
                    className="font-mono"
                  />
                </Field>
              )}
              <div className="min-w-0 sm:col-span-2">
                <Field
                  label="Account selector / query"
                  error={fieldError("query")}
                >
                  <AccountSelectorInput
                    ledger={source.ledger}
                    value={query.address}
                    onChange={(address) => setQuery({ address })}
                    placeholder="accounts:customer:*"
                  />
                </Field>
                <div className="mt-2">
                  <MetaFilterBuilder
                    ledger={source.ledger}
                    rules={query.meta}
                    setRules={(meta) => setQuery({ meta })}
                    combinator={query.metaComb}
                    setCombinator={(metaComb) => setQuery({ metaComb })}
                  />
                </div>
              </div>
              {sourceError && !hasPlacedError && (
                <p className="font-mono text-xs break-words text-destructive sm:col-span-2">
                  {sourceError}
                </p>
              )}
            </div>
          </Card>
        )
      })}
      {!fixedCount && sources.length < maxSources && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() =>
            onChange(addNamedSourceV2(sources, sources[0]?.ledger))
          }
        >
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Add named source
        </Button>
      )}
    </div>
  )
}

function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string
  hint?: string
  error?: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex flex-wrap items-baseline justify-between gap-x-2">
        <Label className="text-xs">{label}</Label>
        {hint && (
          <span className="text-[10px] text-muted-foreground">{hint}</span>
        )}
      </div>
      {children}
      {error && (
        <p className="font-mono text-xs break-words text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}
