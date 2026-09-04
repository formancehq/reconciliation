"use client"

import { useEffect, useState } from "react"
import {
  AlertCircle,
  Check,
  Copy,
  Loader2,
  ShieldAlert,
  ShieldCheck,
  ShieldX,
} from "lucide-react"
import { cn } from "@workspace/ui/lib/utils"
import { reconClient, type AuditEntry } from "@/lib/recon"
import { importEd25519PublicKey, verifyEntry } from "./panels/AuditPanel"

type Verify = "checking" | "valid" | "invalid" | "unsigned" | "nokey"

/**
 * Resolves one alert event's control-ledger write to its signed audit entry
 * (GET /audit/entries/by-transaction/{id}) and verifies the Ed25519 signature
 * in-browser against the published key — the "verify this write" bridge from a
 * business action to its cryptographic proof. Mounted lazily (on row expand),
 * so the fetch only happens when asked.
 */
export function EventAuditProof({ transactionId }: { transactionId: string }) {
  const [entry, setEntry] = useState<AuditEntry | null>(null)
  const [status, setStatus] = useState<"loading" | "ready" | "error">("loading")
  const [verify, setVerify] = useState<Verify>("checking")

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const resolved = await reconClient.getAuditEntryByTransaction(transactionId)
        let result: Verify = "unsigned"
        if (resolved.signed && resolved.payload && resolved.signature && resolved.keyId) {
          const keys = await reconClient.getSigningKeys()
          const match = keys.find((k) => k.keyId === resolved.keyId)
          if (!match) result = "nokey"
          else {
            const key = await importEd25519PublicKey(match.publicKey)
            result = (await verifyEntry(key, resolved)) ? "valid" : "invalid"
          }
        }
        if (!cancelled) {
          setEntry(resolved)
          setVerify(result)
          setStatus("ready")
        }
      } catch {
        if (!cancelled) setStatus("error")
      }
    })()
    return () => {
      cancelled = true
    }
  }, [transactionId])

  if (status === "loading") {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" /> Resolving signed audit entry…
      </div>
    )
  }
  if (status === "error" || !entry) {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <AlertCircle className="h-3.5 w-3.5" /> No audit entry found for this write.
      </div>
    )
  }

  return (
    <div className="space-y-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">Signed audit entry</span>
        <span className="font-mono text-muted-foreground" title="Bucket-wide audit sequence">
          #{entry.sequence}
        </span>
        <VerifyPill verify={verify} />
      </div>
      {entry.keyId && (
        <div className="text-muted-foreground">
          Signing key <span className="font-mono">{entry.keyId}</span>
        </div>
      )}
      {entry.signed && entry.payload && entry.signature ? (
        <div className="space-y-1.5">
          <ProofCopy label="Signed payload (base64)" value={entry.payload} />
          <ProofCopy label="Signature (base64)" value={entry.signature} />
        </div>
      ) : (
        <p className="text-muted-foreground">Unsigned entry — no signature to verify.</p>
      )}
    </div>
  )
}

function VerifyPill({ verify }: { verify: Verify }) {
  const map: Record<Verify, { label: string; className: string; icon: React.ReactNode }> = {
    checking: {
      label: "Verifying…",
      className: "text-muted-foreground",
      icon: <Loader2 className="h-3 w-3 animate-spin" />,
    },
    valid: {
      label: "Signature verified",
      className: "text-green-foreground",
      icon: <ShieldCheck className="h-3 w-3" />,
    },
    invalid: {
      label: "Signature INVALID",
      className: "text-destructive-foreground",
      icon: <ShieldX className="h-3 w-3" />,
    },
    unsigned: {
      label: "Unsigned",
      className: "text-muted-foreground",
      icon: <ShieldAlert className="h-3 w-3" />,
    },
    nokey: {
      label: "Key not published",
      className: "text-amber-foreground",
      icon: <ShieldAlert className="h-3 w-3" />,
    },
  }
  const s = map[verify]
  return (
    <span className={cn("inline-flex items-center gap-1 font-medium", s.className)}>
      {s.icon}
      {s.label}
    </span>
  )
}

function ProofCopy({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-center gap-2 rounded-md bg-muted/40 px-2 py-1.5">
      <div className="min-w-0 flex-1">
        <div className="text-[10px] tracking-wide text-muted-foreground uppercase">{label}</div>
        <div className="truncate font-mono text-[11px]">{value}</div>
      </div>
      <button
        type="button"
        aria-label={`Copy ${label}`}
        className="shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        onClick={() => {
          void navigator.clipboard?.writeText(value)
          setCopied(true)
          setTimeout(() => setCopied(false), 1200)
        }}
      >
        {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      </button>
    </div>
  )
}
