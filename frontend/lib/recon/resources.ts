/**
 * The resource seam the panels talk to.
 *
 * This used to fan every read across two contracts and route every write
 * through a `contractVersion === 2 ? … : …` ternary. V1 is retired, so there is
 * one client and one code path; the seam is kept because the panels are written
 * against it and it is where a future contract would slot in.
 *
 * The V1/V2 unions that used to live here collapsed to the single types they
 * now name, and the vestigial V2 suffix went with them.
 */
import { reconClient } from "./client"
import type {
  AcceptAlertRequest,
  AckAlertRequest,
  AlertEvent,
  ResolveAlertRequest,
  RuleActivity,
  Cursor,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
} from "./common"
import type { Alert, Capture, Rule } from "./types"

/**
 * The contract a record is stamped with. One value is live; the stamp survives
 * because it is written into signed control-ledger metadata, and a record that
 * predates it resolves to the live contract server-side.
 */
export function contractVersionOf(resource: object): 1 | 2 {
  return "contractVersion" in resource && resource.contractVersion === 1 ? 1 : 2
}

export function resourceKey(resource: { id: string } & object): string {
  return `${contractVersionOf(resource)}:${resource.id}`
}

export function ruleResourceKey(resource: { ruleID: string } & object): string {
  return `${contractVersionOf(resource)}:${resource.ruleID}`
}

export async function listAllRules(signal?: AbortSignal): Promise<Rule[]> {
  return signal ? reconClient.listRules(signal) : reconClient.listRules()
}

export async function listAllAlerts(signal?: AbortSignal): Promise<Alert[]> {
  return signal ? reconClient.listAlerts(signal) : reconClient.listAlerts()
}

export async function getRule(
  id: string,
  signal?: AbortSignal
): Promise<Rule> {
  return signal ? reconClient.getRule(id, signal) : reconClient.getRule(id)
}

export async function getAlert(
  id: string,
  signal?: AbortSignal
): Promise<Alert> {
  return signal ? reconClient.getAlert(id, signal) : reconClient.getAlert(id)
}

export async function listCaptures(
  ruleId: string,
  opts?: { period?: string; signal?: AbortSignal }
): Promise<Capture[]> {
  return reconClient.listCaptures(ruleId, opts)
}

export function listRuleTimeline(
  ruleId: string,
  cursor?: string,
  signal?: AbortSignal
): Promise<Cursor<RuleActivity>> {
  return signal
    ? reconClient.listRuleTimeline(ruleId, cursor, signal)
    : reconClient.listRuleTimeline(ruleId, cursor)
}

export function listAlertEvents(
  alertId: string,
  cursor?: string,
  signal?: AbortSignal
): Promise<Cursor<AlertEvent>> {
  return signal
    ? reconClient.listAlertEvents(alertId, cursor, signal)
    : reconClient.listAlertEvents(alertId, cursor)
}

export async function listAlerts(
  signal?: AbortSignal
): Promise<Alert[]> {
  return signal ? reconClient.listAlerts(signal) : reconClient.listAlerts()
}

export async function evaluateRule(
  ruleId: string) {
  return reconClient.evaluateRule(ruleId)
}

export async function patchRuleEnabled(
  rule: Rule,
  enabled: boolean
): Promise<Rule> {
  return reconClient.patchRule(rule.id, { enabled })
}

export async function deleteRule(
  ruleId: string): Promise<void> {
  return reconClient.deleteRule(ruleId)
}

export function acknowledgeAlert(
  alertId: string,
  body: AckAlertRequest
): Promise<Alert> {
  return reconClient.ackAlert(alertId, body)
}

export function resolveAlert(
  alertId: string,
  body: ResolveAlertRequest
): Promise<Alert> {
  return reconClient.resolveAlert(alertId, body)
}

export function acceptAlert(
  alertId: string,
  body: AcceptAlertRequest
): Promise<Alert> {
  return reconClient.acceptAlert(alertId, body)
}

export function snoozeAlert(
  alertId: string,
  body: SnoozeAlertRequest
): Promise<Alert> {
  return reconClient.snoozeAlert(alertId, body)
}

export function unsnoozeAlert(
  alertId: string,
  body: UnsnoozeAlertRequest
): Promise<Alert> {
  return reconClient.unsnoozeAlert(alertId, body)
}
