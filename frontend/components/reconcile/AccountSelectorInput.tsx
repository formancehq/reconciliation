"use client"

/**
 * Account-selector field for the rule builder.
 *
 * In the console this offered a guided segment-tree picker sourced from a live
 * browser→ledger connection. The standalone app has no such connection, so the
 * field is a free-text account selector (reconciliation expresses an address
 * prefix with a trailing `*`). It still shows a loading hint while the ledger's
 * indexed metadata is fetched through the module.
 */
import { Loader2 } from "lucide-react"

import { Input } from "@/components/ui/input"
import { useLedgerMetaFields } from "./useLedgerMetaFields"

/** Reconciliation expresses an address prefix with a trailing `*`. */
export function toReconAddressPrefix(prefix: string): string {
  const value = prefix.trim().replace(/\*+$/, "")
  return value ? `${value}*` : ""
}

export function AccountSelectorInput({
  ledger,
  value,
  onChange,
  placeholder,
  id,
}: {
  ledger: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  id?: string
}) {
  const { loading } = useLedgerMetaFields(ledger)

  return (
    <div className="flex min-w-0 items-center gap-1">
      <Input
        id={id}
        className="min-w-0 flex-1 font-mono text-sm"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
      />
      {loading && (
        <Loader2
          className="size-3.5 shrink-0 animate-spin text-muted-foreground"
          aria-label="Loading account types"
        />
      )}
    </div>
  )
}
