/**
 * The fields of a lettering run's manifest.json the Results tab renders
 * (results doc §6). Amounts are integer minor units written as strings, so
 * they stay exact; `formatMinor` turns them into major units of their asset.
 *
 * The statement renders from the manifest alone (results doc §5), as the UI
 * will render it from the alert's evidence once recon's API exists.
 */

export type LetteringVerdict =
  | "reconciled"
  | "reconciled_with_pending"
  | "reconciled_with_warnings"
  | "breaks"
  | "incomplete"

export interface StatementLine {
  class: string
  outcome: string
  earlierDay?: boolean
  amount: string
  count: number
}

export interface AssetStatement {
  psp: { amount: string; count: number }
  product: { amount: string; count: number }
  net: string
  lines: StatementLine[]
  residual: string
  carriedOutside: StatementLine[]
  flowGross: string
  suspense: {
    openPrev: string
    countPrev: number
    fromLookups: string
    open: string
    count: number
    continuityOk: boolean
  }
  unclassified?: { side: string; state: string; amount: string; count: number }[]
}

export interface Book {
  side: string
  prefix: string
  asset: string
  openSign: string
  openPrev: string
  opened: string
  lettered: string
  letteredOther: string
  open: string
  count: number
  buckets: Record<string, number>
  continuityOk: boolean
}

export interface PaymentAccount {
  account: string
  asset: string
  inputPrev: string
  input: string
  outputPrev: string
  output: string
  flowCredits: string
  flowDebits: string
  creditResidual: string
  debitResidual: string
}

export interface Manifest {
  schemaVersion: string
  engine?: string
  rule: { id: string; version?: number; backfillFrom?: string }
  runId: string
  previousRun?: { runId: string; day: string; manifestSha256: string }
  period: { type: string; day: string; cutoff: string; tz: string }
  startedAt: string
  finishedAt: string
  verdict: LetteringVerdict
  incomplete?: { reason: string; detail: string }
  counts?: {
    flowOutcome?: Record<string, number>
    breaks?: {
      new: number
      persisting: number
      resolved: number
      openByLeg: Record<string, number>
      openByPriority: Record<string, number>
    }
    unclassified?: Record<string, number>
  }
  statement?: Record<string, AssetStatement>
  books?: Book[]
  paymentAccounts?: PaymentAccount[]
}
