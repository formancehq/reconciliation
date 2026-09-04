import { cursorItems, reconRequest } from "./client"
import { collectReconPages } from "./pagination"
import type {
  AckAlertRequest,
  AcceptAlertRequest,
  AlertEventsResponseV2,
  AlertResponseV2,
  AlertsResponseV2,
  CapturesResponseV2,
  EvaluateRuleRequest,
  EvaluationResponseV2,
  ResolveAlertRequest,
  RuleActivitiesResponseV2,
  RulePatchRequestV2,
  RuleRequestV2,
  RuleResponseV2,
  RulesResponseV2,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
} from "./typesV2"

const V2 = "/v2"
const path = (suffix: string) => `${V2}${suffix}`

/** Contract-isolated client for the named-source reconciliation API. */
export const reconClientV2 = {
  async listRules(signal?: AbortSignal) {
    return collectReconPages(async (cursor) => {
      const response = await reconRequest<RulesResponseV2>(
        "GET",
        path("/rules"),
        { query: { pageSize: 1000, cursor }, signal }
      )
      return response.cursor
    })
  },
  async getRule(id: string, signal?: AbortSignal) {
    const response = await reconRequest<RuleResponseV2>(
      "GET",
      path(`/rules/${encodeURIComponent(id)}`),
      { signal }
    )
    return response.data
  },
  async createRule(body: RuleRequestV2) {
    const response = await reconRequest<RuleResponseV2>(
      "POST",
      path("/rules"),
      { body }
    )
    return response.data
  },
  async patchRule(id: string, body: RulePatchRequestV2) {
    const response = await reconRequest<RuleResponseV2>(
      "PATCH",
      path(`/rules/${encodeURIComponent(id)}`),
      { body }
    )
    return response.data
  },
  async deleteRule(id: string) {
    await reconRequest<void>("DELETE", path(`/rules/${encodeURIComponent(id)}`))
  },
  async evaluateRule(id: string, body?: EvaluateRuleRequest) {
    const response = await reconRequest<EvaluationResponseV2>(
      "POST",
      path(`/rules/${encodeURIComponent(id)}/evaluate`),
      { body: body ?? {} }
    )
    return response.data
  },
  async listRuleTimeline(
    ruleId: string,
    cursor?: string,
    signal?: AbortSignal
  ) {
    const response = await reconRequest<RuleActivitiesResponseV2>(
      "GET",
      path(`/rules/${encodeURIComponent(ruleId)}/timeline`),
      { query: { pageSize: 15, cursor }, signal }
    )
    return {
      ...response.cursor,
      data: cursorItems(response.cursor),
    }
  },
  async listAlertEvents(alertId: string, cursor?: string, signal?: AbortSignal) {
    const response = await reconRequest<AlertEventsResponseV2>(
      "GET",
      path(`/alerts/${encodeURIComponent(alertId)}/events`),
      { query: { pageSize: 50, cursor }, signal }
    )
    return { ...response.cursor, data: cursorItems(response.cursor) }
  },
  async listCaptures(
    ruleId: string,
    opts?: { period?: string; signal?: AbortSignal }
  ) {
    return collectReconPages(async (cursor) => {
      const response = await reconRequest<CapturesResponseV2>(
        "GET",
        path(`/rules/${encodeURIComponent(ruleId)}/captures`),
        {
          query: { pageSize: 1000, cursor, period: opts?.period },
          signal: opts?.signal,
        }
      )
      return response.cursor
    })
  },
  async listAlerts(signal?: AbortSignal) {
    return collectReconPages(async (cursor) => {
      const response = await reconRequest<AlertsResponseV2>(
        "GET",
        path("/alerts"),
        { query: { pageSize: 1000, cursor }, signal }
      )
      return response.cursor
    })
  },
  async getAlert(id: string, signal?: AbortSignal) {
    const response = await reconRequest<AlertResponseV2>(
      "GET",
      path(`/alerts/${encodeURIComponent(id)}`),
      { signal }
    )
    return response.data
  },
  async ackAlert(id: string, body: AckAlertRequest) {
    const response = await reconRequest<AlertResponseV2>(
      "POST",
      path(`/alerts/${encodeURIComponent(id)}/ack`),
      { body }
    )
    return response.data
  },
  async resolveAlert(id: string, body: ResolveAlertRequest) {
    const response = await reconRequest<AlertResponseV2>(
      "POST",
      path(`/alerts/${encodeURIComponent(id)}/resolve`),
      { body }
    )
    return response.data
  },
  async acceptAlert(id: string, body: AcceptAlertRequest) {
    const response = await reconRequest<AlertResponseV2>(
      "POST",
      path(`/alerts/${encodeURIComponent(id)}/accept`),
      { body }
    )
    return response.data
  },
  async snoozeAlert(id: string, body: SnoozeAlertRequest) {
    const response = await reconRequest<AlertResponseV2>(
      "POST",
      path(`/alerts/${encodeURIComponent(id)}/snooze`),
      { body }
    )
    return response.data
  },
  async unsnoozeAlert(id: string, body: UnsnoozeAlertRequest) {
    const response = await reconRequest<AlertResponseV2>(
      "POST",
      path(`/alerts/${encodeURIComponent(id)}/unsnooze`),
      { body }
    )
    return response.data
  },
}

export type ReconClientV2 = typeof reconClientV2
