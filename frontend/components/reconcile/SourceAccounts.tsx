"use client"

/**
 * Lists, on demand, the ledger accounts a rule's source selects, with their
 * live balance in the source's asset: the way from a rule or a break back to
 * the ledger data. It runs the source's own query through the module
 * (GET /ledgers/{ledger}/accounts?filter=…) and never on mount.
 */
import { useCallback, useState } from "react"
import { Button } from "@/components/ui/button"
import { ReconError, reconRequest } from "@/lib/recon/client"
import { formatAmount } from "@/lib/recon/display"

const LIMIT = 50

interface AccountsEnvelope {
  data?: { accounts?: { address: string; balances?: Record<string, string> }[] | null; capped?: boolean }
}

export function SourceAccounts({ ledger, query, asset }: { ledger: string; query: unknown; asset: string }) {
  const [accounts, setAccounts] = useState<{ address: string; balance: string }[] | null>(null)
  const [capped, setCapped] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const allAssets = !asset || asset === "*"

  const run = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await reconRequest<AccountsEnvelope>("GET", `/ledgers/${encodeURIComponent(ledger)}/accounts`, {
        query: { filter: JSON.stringify(query ?? {}), limit: LIMIT },
      })
      setAccounts(
        (res.data?.accounts ?? []).map((a) => ({
          address: a.address,
          balance: allAssets ? "" : (a.balances?.[asset] ?? "0"),
        }))
      )
      setCapped(!!res.data?.capped)
    } catch (err) {
      setError(err instanceof ReconError || err instanceof Error ? err.message : "The query could not be run.")
      setAccounts(null)
    } finally {
      setLoading(false)
    }
  }, [ledger, query, asset, allAssets])

  return (
    <div className="mt-2 min-w-0 space-y-1.5 border-t pt-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          Accounts in the ledger
        </span>
        <Button variant="outline" size="sm" onClick={run} disabled={loading} className="h-6 px-2 text-[11px]">
          {loading ? "Reading…" : accounts ? "Refresh" : "Show accounts"}
        </Button>
      </div>
      {error && (
        <p className="rounded border border-destructive-foreground/30 bg-destructive/10 p-2 text-xs text-destructive-foreground">
          {error}
        </p>
      )}
      {accounts && !error && (
        <>
          <p className="text-xs text-muted-foreground">
            {accounts.length === 0
              ? "No account matches now."
              : `${accounts.length} account${accounts.length === 1 ? "" : "s"} in ${ledger}, live balances`}
            {capped && ` · first ${LIMIT} only, narrow the query to see the rest`}
          </p>
          {accounts.length > 0 && (
            <ul className="max-h-48 divide-y overflow-auto rounded border text-xs">
              {accounts.map((a) => (
                <li key={a.address} className="flex items-center justify-between gap-3 px-2 py-1">
                  <span className="min-w-0 font-mono break-all">{a.address}</span>
                  {!allAssets && (
                    <span className="shrink-0 tabular-nums" title={`${a.balance} ${asset}`}>
                      {formatAmount(a.balance, asset)}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  )
}
