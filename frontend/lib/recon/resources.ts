/**
 * The resource seam the panels talk to.
 *
 * This used to fan every read across two contracts and route every write
 * through a `contractVersion === 2 ? … : …` ternary. V1 is retired, so there is
 * one client and one code path; the seam is kept because the panels are written
 * against it and it is where a future contract would slot in.
 *
 * The `Any*` aliases are what remains of the unions — each now names a single
 * type. Renaming them (and dropping the V2 suffix throughout) is cosmetic and
 * deliberately not part of retirement.
 */
import { reconClientV2 } from "./clientV2"
import type {
  AcceptAlertRequest,
  AckAlertRequest,
  AlertEvent,
  ResolveAlertRequest,
  RuleActivity,
  Cursor,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
} from "./types"
import type { AlertV2, CaptureV2, RuleV2 } from "./typesV2"

export type AnyRule = RuleV2
export type AnyAlert = AlertV2
export type AnyCapture = CaptureV2

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

export async function listAllRules(signal?: AbortSignal): Promise<AnyRule[]> {
  return signal ? reconClientV2.listRules(signal) : reconClientV2.listRules()
}

export async function listAllAlerts(signal?: AbortSignal): Promise<AnyAlert[]> {
  return signal ? reconClientV2.listAlerts(signal) : reconClientV2.listAlerts()
}

export async function getRule(
  id: string,
  signal?: AbortSignal
): Promise<AnyRule> {
  return signal ? reconClientV2.getRule(id, signal) : reconClientV2.getRule(id)
}

export async function getAlert(
  id: string,
  signal?: AbortSignal
): Promise<AnyAlert> {
  return signal ? reconClientV2.getAlert(id, signal) : reconClientV2.getAlert(id)
}

export async function listCaptures(
  ruleId: string,
  opts?: { period?: string; signal?: AbortSignal }
): Promise<AnyCapture[]> {
  return reconClientV2.listCaptures(ruleId, opts)
}

export function listRuleTimeline(
  ruleId: string,
  cursor?: string,
  signal?: AbortSignal
): Promise<Cursor<RuleActivity>> {
  return signal
    ? reconClientV2.listRuleTimeline(ruleId, cursor, signal)
    : reconClientV2.listRuleTimeline(ruleId, cursor)
}

export function listAlertEvents(
  alertId: string,
  cursor?: string,
  signal?: AbortSignal
): Promise<Cursor<AlertEvent>> {
  return signal
    ? reconClientV2.listAlertEvents(alertId, cursor, signal)
    : reconClientV2.listAlertEvents(alertId, cursor)
}

export async function listAlerts(
  signal?: AbortSignal
): Promise<AnyAlert[]> {
  return signal ? reconClientV2.listAlerts(signal) : reconClientV2.listAlerts()
}

export async function evaluateRule(
  ruleId: string) {
  return reconClientV2.evaluateRule(ruleId)
}

export async function patchRuleEnabled(
  rule: AnyRule,
  enabled: boolean
): Promise<AnyRule> {
  return reconClientV2.patchRule(rule.id, { enabled })
}

export async function deleteRule(
  ruleId: string): Promise<void> {
  return reconClientV2.deleteRule(ruleId)
}

export function acknowledgeAlert(
  alertId: string,
  body: AckAlertRequest
): Promise<AnyAlert> {
  return reconClientV2.ackAlert(alertId, body)
}

export function resolveAlert(
  alertId: string,
  body: ResolveAlertRequest
): Promise<AnyAlert> {
  return reconClientV2.resolveAlert(alertId, body)
}

export function acceptAlert(
  alertId: string,
  body: AcceptAlertRequest
): Promise<AnyAlert> {
  return reconClientV2.acceptAlert(alertId, body)
}

export function snoozeAlert(
  alertId: string,
  body: SnoozeAlertRequest
): Promise<AnyAlert> {
  return reconClientV2.snoozeAlert(alertId, body)
}

export function unsnoozeAlert(
  alertId: string,
  body: UnsnoozeAlertRequest
): Promise<AnyAlert> {
  return reconClientV2.unsnoozeAlert(alertId, body)
}
