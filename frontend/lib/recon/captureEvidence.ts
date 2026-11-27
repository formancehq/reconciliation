import type { Alert, Capture } from "./types"
import { contractVersionOf } from "./resources"
import { formatAmount } from "./display"

export interface CaptureEvidenceOutcome {
  fingerprint: string
  passed: boolean
  evidence: Record<string, unknown>
}

export interface AlertCaptureEvidenceSelection {
  capture: Capture | undefined
  outcome: CaptureEvidenceOutcome | undefined
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

/**
 * Parse the fingerprint-scoped outcomes retained on a capture. Invalid entries
 * are ignored so a malformed payload cannot be mistaken for alert evidence.
 */
export function captureEvidenceOutcomes(
  evidence: unknown
): CaptureEvidenceOutcome[] {
  if (!Array.isArray(evidence)) return []

  return evidence.flatMap((entry) => {
    if (
      !isRecord(entry) ||
      typeof entry.fingerprint !== "string" ||
      typeof entry.passed !== "boolean" ||
      !isRecord(entry.evidence)
    ) {
      return []
    }

    return [
      {
        fingerprint: entry.fingerprint,
        passed: entry.passed,
        evidence: entry.evidence,
      },
    ]
  })
}

export function findCaptureEvidenceOutcome(
  capture: Capture,
  fingerprint: string
): CaptureEvidenceOutcome | undefined {
  return captureEvidenceOutcomes(capture.evidence).find(
    (outcome) => outcome.fingerprint === fingerprint
  )
}

/**
 * Select evidence for the alert's exact evaluation context. The returned
 * capture can exist without an outcome; callers must treat that as a neutral
 * empty state rather than falling back to the alert's older failure evidence.
 */
export function selectAlertCaptureEvidence(
  captures: Capture[],
  alert: Pick<
    Alert,
    "ruleID" | "periodID" | "lastEvaluationID" | "fingerprint"
  > &
    object
): AlertCaptureEvidenceSelection {
  const capture = captures.find(
    (candidate) =>
      candidate.ruleID === alert.ruleID &&
      contractVersionOf(candidate) === contractVersionOf(alert) &&
      candidate.periodID === alert.periodID &&
      candidate.evaluationID === alert.lastEvaluationID
  )

  return {
    capture,
    outcome: capture
      ? findCaptureEvidenceOutcome(capture, alert.fingerprint)
      : undefined,
  }
}

function text(value: unknown): string | undefined {
  return typeof value === "string" || typeof value === "number" ? String(value) : undefined
}

/**
 * One business sentence for an outcome: how far the checked value is from
 * what the rule allows, in major units. Undefined when the evidence does not
 * carry the figures.
 */
export function outcomeGap(evidence: Record<string, unknown>): string | undefined {
  const asset = text(evidence.asset)
  switch (evidence.operation) {
    case "balance_equation": {
      const off = text(evidence.absoluteResidual)
      if (off === undefined) return undefined
      return `Off by ${formatAmount(off, asset)} · tolerance ${formatAmount(text(evidence.tolerance), asset)}`
    }
    case "source_consensus": {
      const spread = text(evidence.spread)
      if (spread === undefined) return undefined
      return `Sources apart by ${formatAmount(spread, asset)} · tolerance ${formatAmount(text(evidence.tolerance), asset)}`
    }
    case "balance_bounds": {
      const excursion = text(evidence.excursion)
      const source = isRecord(evidence.source) ? evidence.source : undefined
      const balance = text(source?.balance)
      if (excursion && excursion !== "0") return `Outside the bounds by ${formatAmount(excursion, asset)}`
      return balance !== undefined ? `Balance ${formatAmount(balance, asset)}, within bounds` : undefined
    }
    default:
      return undefined
  }
}

/** The gap of a capture's most relevant outcome: the first failing one, else the first. */
export function captureGap(capture: Capture | null | undefined): string | undefined {
  return capture ? evidenceGap(capture.evidence) : undefined
}

/** The gap of an evidence list (a capture's or an evaluation activity's). */
export function evidenceGap(evidence: unknown): string | undefined {
  const outcomes = captureEvidenceOutcomes(evidence)
  const outcome = outcomes.find((o) => !o.passed) ?? outcomes[0]
  return outcome ? outcomeGap(outcome.evidence) : undefined
}
