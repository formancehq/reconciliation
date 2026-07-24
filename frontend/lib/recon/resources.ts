import { ReconError, reconClient } from "./client"
import { reconClientV2 } from "./clientV2"
import type {
  AcceptAlertRequest,
  AckAlertRequest,
  Alert,
  Capture,
  ResolveAlertRequest,
  Rule,
  RuleActivity,
  Cursor,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
} from "./types"
import type { AlertV2, CaptureV2, RuleV2 } from "./typesV2"

export type AnyRule = Rule | RuleV2
export type AnyAlert = Alert | AlertV2
export type AnyCapture = Capture | CaptureV2

export function contractVersionOf(resource: object): 1 | 2 {
  return "contractVersion" in resource && resource.contractVersion === 2 ? 2 : 1
}

export function resourceKey(resource: { id: string } & object): string {
  return `${contractVersionOf(resource)}:${resource.id}`
}

export function ruleResourceKey(resource: { ruleID: string } & object): string {
  return `${contractVersionOf(resource)}:${resource.ruleID}`
}

export function isRuleV2(rule: AnyRule): rule is RuleV2 {
  return contractVersionOf(rule) === 2
}

export function isAlertV2(alert: AnyAlert): alert is AlertV2 {
  return contractVersionOf(alert) === 2
}

export async function listAllRules(signal?: AbortSignal): Promise<AnyRule[]> {
  const [v1, v2] = await Promise.all([
    signal ? reconClient.listRules(signal) : reconClient.listRules(),
    optionalV2(() =>
      signal ? reconClientV2.listRules(signal) : reconClientV2.listRules()
    ),
  ])
  return [...v1, ...v2]
}

export async function listAllAlerts(signal?: AbortSignal): Promise<AnyAlert[]> {
  const [v1, v2] = await Promise.all([
    signal ? reconClient.listAlerts(signal) : reconClient.listAlerts(),
    optionalV2(() =>
      signal ? reconClientV2.listAlerts(signal) : reconClientV2.listAlerts()
    ),
  ])
  return [...v1, ...v2]
}

async function optionalV2<T>(request: () => Promise<T[]>): Promise<T[]> {
  try {
    return await request()
  } catch (error) {
    if (
      error instanceof ReconError &&
      (error.status === 404 || error.status === 405 || error.status === 501)
    )
      return []
    throw error
  }
}

export async function getRuleByContract(
  id: string,
  contractVersion: 1 | 2,
  signal?: AbortSignal
): Promise<AnyRule> {
  return contractVersion === 2
    ? signal
      ? reconClientV2.getRule(id, signal)
      : reconClientV2.getRule(id)
    : signal
      ? reconClient.getRule(id, signal)
      : reconClient.getRule(id)
}

export async function getAlertByContract(
  id: string,
  contractVersion: 1 | 2,
  signal?: AbortSignal
): Promise<AnyAlert> {
  return contractVersion === 2
    ? signal
      ? reconClientV2.getAlert(id, signal)
      : reconClientV2.getAlert(id)
    : signal
      ? reconClient.getAlert(id, signal)
      : reconClient.getAlert(id)
}

export async function listCapturesByContract(
  ruleId: string,
  contractVersion: 1 | 2,
  opts?: { period?: string; signal?: AbortSignal }
): Promise<AnyCapture[]> {
  return contractVersion === 2
    ? reconClientV2.listCaptures(ruleId, opts)
    : reconClient.listCaptures(ruleId, opts)
}

export function listRuleTimelineByContract(
  ruleId: string,
  contractVersion: 1 | 2,
  cursor?: string,
  signal?: AbortSignal
): Promise<Cursor<RuleActivity>> {
  return contractVersion === 2
    ? signal
      ? reconClientV2.listRuleTimeline(ruleId, cursor, signal)
      : reconClientV2.listRuleTimeline(ruleId, cursor)
    : signal
      ? reconClient.listRuleTimeline(ruleId, cursor, signal)
      : reconClient.listRuleTimeline(ruleId, cursor)
}

export async function listAlertsByContract(
  contractVersion: 1 | 2,
  signal?: AbortSignal
): Promise<AnyAlert[]> {
  return contractVersion === 2
    ? signal
      ? reconClientV2.listAlerts(signal)
      : reconClientV2.listAlerts()
    : signal
      ? reconClient.listAlerts(signal)
      : reconClient.listAlerts()
}

export async function evaluateRuleByContract(
  ruleId: string,
  contractVersion: 1 | 2
) {
  return contractVersion === 2
    ? reconClientV2.evaluateRule(ruleId)
    : reconClient.evaluateRule(ruleId)
}

export async function patchRuleEnabled(
  rule: AnyRule,
  enabled: boolean
): Promise<AnyRule> {
  return isRuleV2(rule)
    ? reconClientV2.patchRule(rule.id, { enabled })
    : reconClient.patchRule(rule.id, { enabled })
}

export async function deleteRuleByContract(
  ruleId: string,
  contractVersion: 1 | 2
): Promise<void> {
  return contractVersion === 2
    ? reconClientV2.deleteRule(ruleId)
    : reconClient.deleteRule(ruleId)
}

export function acknowledgeAlertByContract(
  alertId: string,
  contractVersion: 1 | 2,
  body: AckAlertRequest
): Promise<AnyAlert> {
  return contractVersion === 2
    ? reconClientV2.ackAlert(alertId, body)
    : reconClient.ackAlert(alertId, body)
}

export function resolveAlertByContract(
  alertId: string,
  contractVersion: 1 | 2,
  body: ResolveAlertRequest
): Promise<AnyAlert> {
  return contractVersion === 2
    ? reconClientV2.resolveAlert(alertId, body)
    : reconClient.resolveAlert(alertId, body)
}

export function acceptAlertByContract(
  alertId: string,
  contractVersion: 1 | 2,
  body: AcceptAlertRequest
): Promise<AnyAlert> {
  return contractVersion === 2
    ? reconClientV2.acceptAlert(alertId, body)
    : reconClient.acceptAlert(alertId, body)
}

export function snoozeAlertByContract(
  alertId: string,
  contractVersion: 1 | 2,
  body: SnoozeAlertRequest
): Promise<AnyAlert> {
  return contractVersion === 2
    ? reconClientV2.snoozeAlert(alertId, body)
    : reconClient.snoozeAlert(alertId, body)
}

export function unsnoozeAlertByContract(
  alertId: string,
  contractVersion: 1 | 2,
  body: UnsnoozeAlertRequest
): Promise<AnyAlert> {
  return contractVersion === 2
    ? reconClientV2.unsnoozeAlert(alertId, body)
    : reconClient.unsnoozeAlert(alertId, body)
}
