"use client"

/**
 * Audit — the "prove it" surface (EN-1930).
 *
 * Two things an auditor needs, and nothing they don't:
 *   1. A verification panel: the public signing key + a recipe, so they can
 *      verify any control-ledger entry themselves — without trusting us.
 *   2. A signed-activity feed: who handled which break, with a verified-vs-
 *      declared provenance badge sourced from the signed metadata (P1.2).
 *
 * v1 sources the feed from the alerts list (their current lifecycle + actor).
 * A richer per-entry stream with sequence numbers + in-browser signature checks
 * arrives with P1.3 (serving the ledger's audit entries).
 */
import { useState } from "react"
import { ShieldCheck, Check, Copy, ChevronRight, KeyRound } from "lucide-react"
import { Card } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
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
} from "@/lib/recon"
import { Loading, ErrorState, SeverityBadge, StatusBadge, EmptyState } from "../ui"

interface Data {
  keys: SigningKey[]
  alerts: AnyAlert[]
  rules: AnyRule[]
}

export function AuditPanel() {
  const res = useReconResource<Data>(async (signal) => {
    const [keys, alerts, rules] = await Promise.all([
      reconClient.getSigningKeys(signal),
      listAllAlerts(signal),
      listAllRules(signal),
    ])
    return { keys, alerts, rules }
  }, [])

  if (res.loading) return <Loading label="Loading audit trail…" />
  if (res.error) return <ErrorState error={res.error} onRetry={res.refetch} />

  const keys = res.data?.keys ?? []
  const alerts = res.data?.alerts ?? []
  const rules = res.data?.rules ?? []

  const ruleName = (a: AnyAlert) =>
    rules.find(
      (r) => r.id === a.ruleID && contractVersionOf(r) === contractVersionOf(a)
    )?.name ?? a.ruleID

  // The feed: alerts that carry a signed handling action, most-recent first.
  const handled = alerts
    .filter((a) => alertActor(a) !== undefined)
    .sort(
      (a, b) =>
        new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()
    )

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-3 sm:p-4">
      <VerificationCard keys={keys} />

      <section>
        <h3 className="mb-3 text-sm font-semibold">Signed activity</h3>
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
        <p className="mt-3 flex items-start gap-1.5 text-xs text-muted-foreground">
          <ShieldCheck className="mt-px h-3.5 w-3.5 shrink-0" />
          <span>
            <span className="font-medium">Verified</span> means the actor is the
            subject of an authenticated token, bound into the signature.{" "}
            <span className="font-medium">Declared</span> is a self-typed name —
            kept for display, not a trust anchor.
          </span>
        </p>
      </section>
    </div>
  )
}

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
            <li>Read a control-ledger entry — its signed payload (the batch bytes) and its Ed25519 signature.</li>
            <li>
              Check <code className="font-mono text-xs">ed25519.Verify(publicKey, payload, signature)</code>
              . It passes only if reconciliation wrote that exact entry and nothing altered it since.
            </li>
          </ol>
          <p>
            The check needs nobody from Formance and no access to our systems — the
            public key is the whole trust anchor.
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
