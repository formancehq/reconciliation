"use client"

/**
 * Audit — the "prove it" surface (EN-1930).
 *
 *   1. Verification panel: the public signing key + a recipe, so anyone can
 *      verify any control-ledger entry themselves — without trusting us.
 *   2. Audit trail: the ledger's own signed entries (GET /audit/entries) with a
 *      one-click in-browser check — the JS twin of ed25519.Verify. Nothing here
 *      is recomputed by us; the signatures are the ledger's, verified client-side.
 *   3. Handling activity: who worked which break, with a verified-vs-declared
 *      provenance badge from the signed metadata (P1.2).
 */
import { useState } from "react"
import {
  ShieldCheck,
  Check,
  Copy,
  ChevronRight,
  KeyRound,
  Hash,
  CircleCheck,
  CircleX,
  Loader2,
} from "lucide-react"
import { Card } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { cn } from "@workspace/ui/lib/utils"
import {
  useReconResource,
  reconClient,
  listAllAlerts,
  listAllRules,
  contractVersionOf,
  formatRelative,
  formatDateTime,
  resourceKey,
  type AnyAlert,
  type AnyRule,
  type Actor,
  type SigningKey,
  type AuditEntry,
} from "@/lib/recon"
import { Loading, ErrorState, SeverityBadge, StatusBadge, EmptyState } from "../ui"

interface Data {
  keys: SigningKey[]
  entries: AuditEntry[]
  alerts: AnyAlert[]
  rules: AnyRule[]
}

export function AuditPanel() {
  const res = useReconResource<Data>(async (signal) => {
    const [keys, entries, alerts, rules] = await Promise.all([
      reconClient.getSigningKeys(signal),
      reconClient.getAuditEntries(100, signal),
      listAllAlerts(signal),
      listAllRules(signal),
    ])
    return { keys, entries, alerts, rules }
  }, [])

  if (res.loading) return <Loading label="Loading audit trail…" />
  if (res.error) return <ErrorState error={res.error} onRetry={res.refetch} />

  const keys = res.data?.keys ?? []
  const entries = res.data?.entries ?? []
  const alerts = res.data?.alerts ?? []
  const rules = res.data?.rules ?? []
  const publicKey = keys[0]?.publicKey

  const ruleName = (a: AnyAlert) =>
    rules.find(
      (r) => r.id === a.ruleID && contractVersionOf(r) === contractVersionOf(a)
    )?.name ?? a.ruleID

  const handled = alerts
    .filter((a) => alertActor(a) !== undefined)
    .sort(
      (a, b) =>
        new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()
    )

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-3 sm:p-4">
      <VerificationCard keys={keys} />

      <AuditTrailCard entries={entries} publicKey={publicKey} />

      <section>
        <h3 className="mb-3 text-sm font-semibold">Who handled what</h3>
        {handled.length === 0 ? (
          <EmptyState icon={<ShieldCheck className="h-5 w-5" />} title="No signed handling yet">
            Acknowledge, resolve, or accept a break and it shows up here — bound to
            whoever did it, inside the signature.
          </EmptyState>
        ) : (
          <ul className="divide-y rounded-md border">
            {handled.map((a) => {
              const actor = alertActor(a)!
              return (
                <li
                  key={resourceKey(a)}
                  className="flex flex-wrap items-center gap-3 px-3 py-2.5"
                >
                  <SeverityBadge severity={a.severity} />
                  <div className="min-w-40 flex-1 basis-40">
                    <div className="truncate font-mono text-sm">
                      {a.fingerprint}
                    </div>
                    <div className="truncate text-xs text-muted-foreground">
                      {ruleName(a)}
                    </div>
                  </div>
                  <StatusBadge status={a.status} />
                  <div className="flex min-w-40 items-center gap-1.5">
                    <span className="truncate text-sm">{actorName(a)}</span>
                    <ActorBadge actor={actor} />
                  </div>
                  <span
                    className="shrink-0 text-xs text-muted-foreground"
                    title={a.updatedAt}
                  >
                    {formatRelative(a.updatedAt)}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </section>
    </div>
  )
}

// ── Audit trail (the real signed entries + in-browser verify) ────────────────

type VerifyState = "checking" | "valid" | "invalid"
type TrailFilter = "all" | "committed" | "rejected"

function AuditTrailCard({
  entries,
  publicKey,
}: {
  entries: AuditEntry[]
  publicKey?: string
}) {
  const [results, setResults] = useState<Record<number, VerifyState>>({})
  const [running, setRunning] = useState(false)
  const [unsupported, setUnsupported] = useState(false)
  const [filter, setFilter] = useState<TrailFilter>("all")
  const [expanded, setExpanded] = useState<Record<number, boolean>>({})
  // The list omits the failure reason/message (and per-order detail); fetch the
  // full entry lazily when a row is first expanded.
  const [details, setDetails] = useState<Record<number, AuditEntry>>({})

  const toggle = (seq: number) => {
    const willOpen = !expanded[seq]
    setExpanded((m) => ({ ...m, [seq]: !m[seq] }))
    if (willOpen && details[seq] === undefined) {
      reconClient.getAuditEntry(seq).then(
        (full) => setDetails((d) => ({ ...d, [seq]: full })),
        () => {} // best-effort — the row's list fields still render
      )
    }
  }

  const verifiable = entries.filter((e) => e.signed && e.payload && e.signature)

  async function verifyAll() {
    if (!publicKey || running) return
    setRunning(true)
    setResults({})
    try {
      const key = await importEd25519PublicKey(publicKey)
      const next: Record<number, VerifyState> = {}
      for (const e of verifiable) {
        next[e.sequence] = (await verifyEntry(key, e)) ? "valid" : "invalid"
        setResults({ ...next })
      }
    } catch {
      // Web Crypto Ed25519 not available in this browser — fall back to the recipe.
      setUnsupported(true)
    } finally {
      setRunning(false)
    }
  }

  const checked = Object.values(results)
  const validCount = checked.filter((s) => s === "valid").length
  const invalidCount = checked.filter((s) => s === "invalid").length

  const committedCount = entries.filter((e) => e.outcome !== "failure").length
  const rejectedCount = entries.filter((e) => e.outcome === "failure").length
  const shown = entries.filter((e) =>
    filter === "all"
      ? true
      : filter === "rejected"
        ? e.outcome === "failure"
        : e.outcome !== "failure"
  )

  return (
    <Card className="space-y-3 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Hash className="h-5 w-5 text-muted-foreground" />
        <span className="text-base font-medium">Audit trail</span>
        <span className="text-xs text-muted-foreground">
          {entries.length} most recent {entries.length === 1 ? "write" : "writes"}
        </span>
        <div className="ml-auto flex items-center gap-2">
          {invalidCount > 0 ? (
            <span className="text-xs font-medium text-destructive-foreground">
              {invalidCount} failed
            </span>
          ) : checked.length > 0 && !running ? (
            <span className="text-xs font-medium text-green-foreground">
              {validCount} verified
            </span>
          ) : null}
          <Button
            size="sm"
            variant="outline"
            onClick={verifyAll}
            disabled={!publicKey || running || verifiable.length === 0}
          >
            {running ? (
              <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
            ) : (
              <ShieldCheck className="mr-1.5 h-3.5 w-3.5" />
            )}
            {running ? "Verifying…" : "Verify all"}
          </Button>
        </div>
      </div>

      {/* Filter by outcome. Both committed and rejected writes are signed — the
          signature verifies regardless; this filters what the ledger *applied*. */}
      <div className="flex flex-wrap items-center gap-1.5">
        <TrailFilterChip label="All" count={entries.length} active={filter === "all"} onClick={() => setFilter("all")} />
        <TrailFilterChip label="Committed" count={committedCount} active={filter === "committed"} onClick={() => setFilter("committed")} />
        <TrailFilterChip label="Rejected" count={rejectedCount} active={filter === "rejected"} onClick={() => setFilter("rejected")} />
      </div>

      {unsupported && (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          In-browser verification isn’t available here. Use the recipe above with
          any Ed25519 tool — the entries below carry the payload and signature.
        </p>
      )}

      {shown.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">
          {entries.length === 0
            ? "No signed entries yet. Evaluate a rule or work a break to record one."
            : "No entries match this filter."}
        </p>
      ) : (
        <ul className="divide-y overflow-hidden rounded-md border">
          {shown.map((e) => {
            const isOpen = !!expanded[e.sequence]
            return (
              <li key={e.sequence} className="min-w-0">
                <button
                  type="button"
                  onClick={() => toggle(e.sequence)}
                  aria-expanded={isOpen}
                  className="flex w-full items-center gap-3 px-3 py-2 text-left transition-colors hover:bg-muted/25"
                >
                  <ChevronRight
                    className={cn(
                      "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                      isOpen && "rotate-90"
                    )}
                  />
                  <span className="w-14 shrink-0 font-mono text-xs text-muted-foreground">
                    #{e.sequence}
                  </span>
                  <div className="min-w-0 flex-1">
                    <span className="text-sm">
                      {e.outcome === "failure" ? "Rejected" : "Committed"}
                    </span>
                    {e.outcome === "failure" && e.failureReason && (
                      <span className="ml-2 text-xs text-destructive-foreground">
                        {e.failureReason}
                      </span>
                    )}
                    <span className="ml-2 text-xs text-muted-foreground">
                      · {e.orderCount} action{e.orderCount === 1 ? "" : "s"}
                    </span>
                  </div>
                  <VerifyCell signed={e.signed} state={results[e.sequence]} />
                  <span
                    className="w-20 shrink-0 text-right text-xs text-muted-foreground"
                    title={e.timestamp}
                  >
                    {e.timestamp ? formatRelative(e.timestamp) : "—"}
                  </span>
                </button>
                {isOpen && <AuditEntryDetail entry={details[e.sequence] ?? e} />}
              </li>
            )
          })}
        </ul>
      )}

      <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
        <Hash className="mt-px h-3.5 w-3.5 shrink-0" />
        <span>
          <span className="font-medium">#sequence</span> is the ledger’s global
          audit position, shared with its other ledgers — so numbers skip here,
          and that’s expected. <span className="font-medium">Verify all</span>{" "}
          checks each entry’s signature on its own; whether the record is
          <em> complete</em> is a separate question answered against the ledger’s
          own audit trail, not these numbers.
        </span>
      </p>
    </Card>
  )
}

function TrailFilterChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string
  count: number
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "rounded-full border px-2.5 py-1 text-xs transition-colors",
        active
          ? "border-transparent bg-primary text-primary-foreground"
          : "text-muted-foreground hover:bg-accent"
      )}
    >
      {label} <span className="tabular-nums opacity-70">{count}</span>
    </button>
  )
}

// AuditEntryDetail is the expanded row: the facts a consumer needs plus the raw
// {payload, signature} so an auditor can verify with their own Ed25519 tooling,
// and the ledger's own reason when a write was rejected.
function AuditEntryDetail({ entry }: { entry: AuditEntry }) {
  return (
    <div className="space-y-2 border-t bg-muted/15 px-3 py-3 pl-10 text-xs">
      <DetailRow label="Recorded" value={entry.timestamp ? formatDateTime(entry.timestamp) : "—"} />
      {entry.actions && entry.actions.length > 0 ? (
        <div className="flex flex-wrap gap-x-2">
          <span className="w-28 shrink-0 text-muted-foreground">Actions</span>
          <div className="flex min-w-0 flex-col gap-1">
            {entry.actions.map((a, i) => (
              <div key={i} className="flex flex-wrap items-baseline gap-x-1.5">
                <span className="font-medium">{a.kind}</span>
                {a.ledger && <span className="text-muted-foreground">· {a.ledger}</span>}
                {a.detail && <span className="font-mono text-muted-foreground">· {a.detail}</span>}
              </div>
            ))}
          </div>
        </div>
      ) : (
        <DetailRow label="Actions" value={`${entry.orderCount} order${entry.orderCount === 1 ? "" : "s"}`} />
      )}
      {entry.ledgers && entry.ledgers.length > 0 && (
        <DetailRow label="Ledgers" value={entry.ledgers.join(", ")} />
      )}
      {entry.keyId && <DetailRow label="Signing key" value={entry.keyId} mono />}

      {entry.outcome === "failure" && (entry.failureReason || entry.failureMessage) && (
        <div className="rounded-md border border-destructive/40 bg-muted/40 px-2.5 py-2">
          <div className="font-medium text-destructive-foreground">
            Rejected by the ledger{entry.failureReason ? ` · ${entry.failureReason}` : ""}
          </div>
          {entry.failureMessage && (
            <p className="mt-0.5 break-words text-muted-foreground">
              {entry.failureMessage}
            </p>
          )}
        </div>
      )}

      {entry.signed && entry.payload && entry.signature ? (
        <div className="space-y-1.5">
          <CopyField label="Signed payload (base64)" value={entry.payload} />
          <CopyField label="Signature (base64)" value={entry.signature} />
        </div>
      ) : (
        <p className="text-muted-foreground">Unsigned entry — no signature to verify.</p>
      )}
    </div>
  )
}

function DetailRow({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="flex flex-wrap gap-x-2">
      <span className="w-28 shrink-0 text-muted-foreground">{label}</span>
      <span className={cn("min-w-0 break-all", mono && "font-mono")}>{value}</span>
    </div>
  )
}

function CopyField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-muted/40 px-2 py-1.5">
      <span className="w-28 shrink-0 text-muted-foreground">{label}</span>
      <code className="min-w-0 flex-1 truncate font-mono text-[11px]">{value}</code>
      <CopyButton value={value} label={label} />
    </div>
  )
}

function VerifyCell({
  signed,
  state,
}: {
  signed: boolean
  state?: VerifyState
}) {
  if (!signed)
    return <span className="w-24 shrink-0 text-right text-xs text-muted-foreground">unsigned</span>
  if (state === "valid")
    return (
      <span className="flex w-24 shrink-0 items-center justify-end gap-1 text-xs text-green-foreground">
        <CircleCheck className="h-3.5 w-3.5" /> verified
      </span>
    )
  if (state === "invalid")
    return (
      <span className="flex w-24 shrink-0 items-center justify-end gap-1 text-xs text-destructive-foreground">
        <CircleX className="h-3.5 w-3.5" /> failed
      </span>
    )
  if (state === "checking")
    return (
      <span className="flex w-24 shrink-0 items-center justify-end text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
      </span>
    )
  return <span className="w-24 shrink-0 text-right text-xs text-muted-foreground">signed</span>
}

// ── Web Crypto Ed25519 — the exact check an external auditor runs ────────────

// Return an ArrayBuffer (unambiguously a BufferSource) so Web Crypto's typed
// signatures are satisfied without wrestling the Uint8Array<ArrayBufferLike>
// generics that newer TS lib types introduce.
function b64ToBuffer(b64: string): ArrayBuffer {
  const bin = atob(b64)
  const buf = new ArrayBuffer(bin.length)
  const view = new Uint8Array(buf)
  for (let i = 0; i < bin.length; i++) view[i] = bin.charCodeAt(i)
  return buf
}

export async function importEd25519PublicKey(b64: string): Promise<CryptoKey> {
  return crypto.subtle.importKey("raw", b64ToBuffer(b64), { name: "Ed25519" }, false, [
    "verify",
  ])
}

export async function verifyEntry(key: CryptoKey, entry: AuditEntry): Promise<boolean> {
  if (!entry.payload || !entry.signature) return false
  return crypto.subtle.verify(
    { name: "Ed25519" },
    key,
    b64ToBuffer(entry.signature),
    b64ToBuffer(entry.payload)
  )
}

// ── Verification panel + attribution helpers ─────────────────────────────────

function VerificationCard({ keys }: { keys: SigningKey[] }) {
  return (
    <Card className="space-y-4 p-4">
      <div className="flex items-center gap-2">
        <ShieldCheck className="h-5 w-5 text-green-foreground" />
        <span className="text-base font-medium">Independently verifiable</span>
        {keys.length > 0 && (
          <Badge variant="secondary" className="ml-auto">
            signing on
          </Badge>
        )}
      </div>
      <p className="text-sm text-muted-foreground">
        Every action here is signed on the control ledger. Verify it yourself with
        the public key below — no need to trust Formance or the ledger operator.
      </p>

      {keys.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-4 text-center text-sm text-muted-foreground">
          No signing key registered. Writes are unsigned on this deployment.
        </p>
      ) : (
        <div className="space-y-2">
          {keys.map((k) => (
            <div
              key={k.keyId}
              className="flex flex-wrap items-center gap-2 rounded-md bg-muted/50 px-3 py-2"
            >
              <KeyRound className="h-4 w-4 shrink-0 text-muted-foreground" />
              <code className="font-mono text-xs text-muted-foreground">
                {k.keyId}
              </code>
              <code className="min-w-0 flex-1 truncate font-mono text-xs">
                {k.publicKey}
              </code>
              <CopyButton value={k.publicKey} label="public key" />
            </div>
          ))}
        </div>
      )}

      <HowToVerify hasKey={keys.length > 0} />
    </Card>
  )
}

function HowToVerify({ hasKey }: { hasKey: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-md border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm font-medium transition-colors hover:bg-accent"
        aria-expanded={open}
      >
        <ChevronRight
          className={cn("h-4 w-4 transition-transform", open && "rotate-90")}
        />
        How to verify
      </button>
      {open && (
        <div className="space-y-3 border-t px-3 py-3 text-sm text-muted-foreground">
          <ol className="ml-4 list-decimal space-y-1.5">
            <li>Take the base64 public key above{hasKey ? "" : " (once a key is registered)"}.</li>
            <li>Read a control-ledger entry from the audit trail — its signed payload (the batch bytes) and its Ed25519 signature.</li>
            <li>
              Check <code className="font-mono text-xs">ed25519.Verify(publicKey, payload, signature)</code>
              . It passes only if reconciliation wrote that exact entry and nothing altered it since.
            </li>
          </ol>
          <p>
            The <span className="font-medium">Verify all</span> button below runs
            exactly this check in your browser. It needs nobody from Formance and
            no access to our systems — the public key is the whole trust anchor.
          </p>
        </div>
      )}
    </div>
  )
}

function ActorBadge({ actor }: { actor: Actor }) {
  if (actor.source === "token") {
    return (
      <Badge variant="secondary" className="gap-1 text-green-foreground">
        <ShieldCheck className="h-3 w-3" /> verified
      </Badge>
    )
  }
  return <Badge variant="outline">declared</Badge>
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value)
          setCopied(true)
          setTimeout(() => setCopied(false), 1500)
        } catch {
          /* clipboard blocked — no-op */
        }
      }}
      aria-label={`Copy ${label}`}
      className="flex shrink-0 items-center gap-1 rounded-md border px-2 py-1 text-xs transition-colors hover:bg-accent"
    >
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      {copied ? "copied" : "copy"}
    </button>
  )
}

function alertActor(a: AnyAlert): Actor | undefined {
  return a.resolution?.actor ?? a.ack?.actor
}

function actorName(a: AnyAlert): string {
  return a.resolution?.by ?? a.ack?.by ?? "—"
}
