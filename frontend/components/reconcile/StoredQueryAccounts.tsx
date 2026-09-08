"use client"

/**
 * Runs the account query an alert recorded, on demand.
 *
 * An aggregate alert reports a count and a total and deliberately embeds no
 * account list — a list would grow with the size of the problem, and it would
 * be stale by the time anyone read it. What it records instead is the query it
 * ran (`effectiveQuery`), which this component hands back to the module that
 * wrote it: GET /ledgers/{ledger}/accounts?filter=…
 *
 * On demand, and never on mount: rendering a page of alerts must not fire a
 * ledger query per alert.
 *
 * Errors are shown, not swallowed. The backend reports why a filtered read
 * failed — an unindexed metadata key being the common case, with the ledger's
 * own message naming the field — and an operator asking "which holds?" needs
 * that reason rather than an empty table.
 */
import { useCallback, useState } from "react"
import { Button } from "@/components/ui/button"
import { ReconError, reconRequest } from "@/lib/recon/client"

interface QueriedAccount {
  address: string
  balances?: Record<string, string>
}

interface AccountsEnvelope {
  data?: { accounts?: QueriedAccount[] | null; capped?: boolean }
}

export function StoredQueryAccounts({
  ledger,
  filter,
  asset,
  /** What the evaluation counted, so the rows can be reconciled against it. */
  matched,
  flagged,
}: {
  ledger: string
  filter: string
  asset: string
  matched: number
  flagged: number
}) {
  const [accounts, setAccounts] = useState<QueriedAccount[] | null>(null)
  const [capped, setCapped] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const run = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await reconRequest<AccountsEnvelope>(
        "GET",
        `/ledgers/${encodeURIComponent(ledger)}/accounts`,
        { query: { filter, limit: 200 } }
      )
      setAccounts(res.data?.accounts ?? [])
      setCapped(!!res.data?.capped)
    } catch (err) {
      setError(
        err instanceof ReconError || err instanceof Error
          ? err.message
          : "The query could not be run."
      )
      setAccounts(null)
    } finally {
      setLoading(false)
    }
  }, [ledger, filter])

  // A matched account holding nothing is a released hold: it kept its deadline
  // metadata, so the query still selects it, and only its balance says it is
  // gone. Showing that split is what makes the row count reconcile with the
  // alert's holdsFlagged / holdsReleased.
  const holding = (accounts ?? []).filter(
    (a) => (a.balances?.[asset] ?? "0") !== "0"
  )
  const released = (accounts?.length ?? 0) - holding.length

  return (
    <div className="min-w-0 space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          The accounts behind this alert
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={run}
          disabled={loading}
          className="h-7 text-xs"
        >
          {loading ? "Reading…" : accounts ? "Refresh" : "List them"}
        </Button>
      </div>

      <p className="text-xs text-muted-foreground">
        Runs the query this evaluation made against{" "}
        <span className="font-mono">{ledger}</span>, cutoff included. Balances
        always read live, so it answers &ldquo;still past that cutoff and still
        funded&rdquo; rather than replaying the run — expect drift if holds have
        been released since.
      </p>

      {error && (
        <p className="rounded border border-destructive/40 bg-destructive/5 p-2 text-xs text-destructive">
          {error}
        </p>
      )}

      {accounts && !error && (
        <div className="space-y-1">
          <div className="text-xs text-muted-foreground">
            {accounts.length === 0
              ? "Nothing matches now — the holds this alert counted have since been released or their deadlines revised."
              : `${accounts.length === 1 ? "1 account matches" : `${accounts.length} accounts match`} now · ${holding.length} still holding funds${released > 0 ? ` · ${released} released` : ""}`}
            {accounts.length > 0 &&
              (accounts.length !== matched || holding.length !== flagged) && (
                <>
                  {" "}
                  <span className="text-amber-600 dark:text-amber-500">
                    (the run saw {matched} matched, {flagged} flagged)
                  </span>
                </>
              )}
          </div>
          {capped && (
            <p className="text-xs text-amber-600 dark:text-amber-500">
              Truncated at 200 accounts — narrow the query to see the rest.
            </p>
          )}
          {accounts.length > 0 && (
            <div className="max-h-64 overflow-auto">
              <table className="w-full min-w-0 text-sm">
                <tbody>
                  {accounts.map((account) => {
                    const balance = account.balances?.[asset] ?? "0"
                    const isReleased = balance === "0"
                    return (
                      <tr
                        key={account.address}
                        className="border-b last:border-b-0"
                      >
                        <td className="py-1 pr-3 font-medium break-all">
                          {account.address}
                        </td>
                        <td className="py-1 pr-3 whitespace-nowrap tabular-nums">
                          {balance} {asset}
                        </td>
                        <td className="py-1 whitespace-nowrap text-muted-foreground">
                          {isReleased ? "released" : ""}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <details className="text-xs">
        <summary className="cursor-pointer text-muted-foreground">
          Show the query
        </summary>
        <pre className="mt-1 overflow-x-auto rounded bg-muted/50 p-2 text-[11px] leading-relaxed">
          {filter}
        </pre>
      </details>
    </div>
  )
}
