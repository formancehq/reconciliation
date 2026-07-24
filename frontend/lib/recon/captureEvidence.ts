import type { AnyAlert, AnyCapture } from "./resources"
import { contractVersionOf } from "./resources"

export interface CaptureEvidenceOutcome {
  fingerprint: string
  passed: boolean
  evidence: Record<string, unknown>
}

export interface AlertCaptureEvidenceSelection {
  capture: AnyCapture | undefined
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
  capture: AnyCapture,
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
  captures: AnyCapture[],
  alert: Pick<
    AnyAlert,
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
