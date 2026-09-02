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
      reconClient.getAuditEntries(50, signal),
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

  return (
    <Card className="space-y-3 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Hash className="h-5 w-5 text-muted-foreground" />
        <span className="text-base font-medium">Audit trail</span>
        <span className="text-xs text-muted-foreground">
          {entries.length} most recent signed {entries.length === 1 ? "write" : "writes"}
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

      {unsupported && (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          In-browser verification isn’t available here. Use the recipe above with
          any Ed25519 tool — the entries below carry the payload and signature.
        </p>
      )}

      {entries.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-6 text-center text-sm text-muted-foreground">
          No signed entries yet. Evaluate a rule or work a break to record one.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <ul className="divide-y rounded-md border">
            {entries.map((e) => (
              <li
                key={e.sequence}
                className="flex flex-wrap items-center gap-3 px-3 py-2"
              >
                <span className="w-16 shrink-0 font-mono text-xs text-muted-foreground">
                  #{e.sequence}
                </span>
                <div className="min-w-32 flex-1">
                  <span className="text-sm">
                    {e.outcome === "failure" ? "Rejected write" : "Signed write"}
                  </span>
                  {e.outcome === "failure" && (
                    <Badge variant="outline" className="ml-2 text-destructive-foreground">
                      failed
                    </Badge>
                  )}
                </div>
                <VerifyCell signed={e.signed} state={results[e.sequence]} />
                <span
                  className="w-20 shrink-0 text-right text-xs text-muted-foreground"
                  title={e.timestamp}
                >
                  {e.timestamp ? formatRelative(e.timestamp) : "—"}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
        <Hash className="mt-px h-3.5 w-3.5 shrink-0" />
        <span>
          <span className="font-medium">#sequence</span> is the ledger’s global
          audit position, shared with other ledgers in the bucket — so numbers may
          skip. Each entry’s signature is verified on its own; contiguity is a
          separate, per-bucket property.
        </span>
      </p>
    </Card>
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

async function importEd25519PublicKey(b64: string): Promise<CryptoKey> {
  return crypto.subtle.importKey("raw", b64ToBuffer(b64), { name: "Ed25519" }, false, [
    "verify",
  ])
}

async function verifyEntry(key: CryptoKey, entry: AuditEntry): Promise<boolean> {
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
