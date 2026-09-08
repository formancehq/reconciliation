import { cursorItems, reconRequest } from "./client"
import { collectReconPages } from "./pagination"
import type {
  AckAlertRequest,
  AcceptAlertRequest,
  EvaluateRuleRequest,
  ResolveAlertRequest,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
} from "./types"
import type {
  AlertEventsResponse,
  AlertResponse,
  AlertsResponse,
  CapturesResponse,
  EvaluationResponse,
  RuleActivitiesResponse,
  RulePatchRequest,
  RuleRequest,
  RuleResponse,
  RulesResponse,
} from "./typesV2"

/** Contract-isolated client for the named-source reconciliation API. */
export const reconClientV2 = {
  async listRules(signal?: AbortSignal) {
    return collectReconPages(async (cursor) => {
      const response = await reconRequest<RulesResponse>(
        "GET",
        "/rules",
        { query: { pageSize: 1000, cursor }, signal }
      )
      return response.cursor
    })
  },
  async getRule(id: string, signal?: AbortSignal) {
    const response = await reconRequest<RuleResponse>(
      "GET",
      `/rules/${encodeURIComponent(id)}`,
      { signal }
    )
    return response.data
  },
  async createRule(body: RuleRequest) {
    const response = await reconRequest<RuleResponse>(
      "POST",
      "/rules",
      { body }
    )
    return response.data
  },
  async patchRule(id: string, body: RulePatchRequest) {
    const response = await reconRequest<RuleResponse>(
      "PATCH",
      `/rules/${encodeURIComponent(id)}`,
      { body }
    )
    return response.data
  },
  async deleteRule(id: string) {
    await reconRequest<void>("DELETE", `/rules/${encodeURIComponent(id)}`)
  },
  async evaluateRule(id: string, body?: EvaluateRuleRequest) {
    const response = await reconRequest<EvaluationResponse>(
      "POST",
      `/rules/${encodeURIComponent(id)}/evaluate`,
      { body: body ?? {} }
    )
    return response.data
  },
  async listRuleTimeline(
    ruleId: string,
    cursor?: string,
    signal?: AbortSignal
  ) {
    const response = await reconRequest<RuleActivitiesResponse>(
      "GET",
      `/rules/${encodeURIComponent(ruleId)}/timeline`,
      { query: { pageSize: 15, cursor }, signal }
    )
    return {
      ...response.cursor,
      data: cursorItems(response.cursor),
    }
  },
  async listAlertEvents(alertId: string, cursor?: string, signal?: AbortSignal) {
    const response = await reconRequest<AlertEventsResponse>(
      "GET",
      `/alerts/${encodeURIComponent(alertId)}/events`,
      { query: { pageSize: 50, cursor }, signal }
    )
    return { ...response.cursor, data: cursorItems(response.cursor) }
  },
  async listCaptures(
    ruleId: string,
    opts?: { period?: string; signal?: AbortSignal }
  ) {
    return collectReconPages(async (cursor) => {
      const response = await reconRequest<CapturesResponse>(
        "GET",
        `/rules/${encodeURIComponent(ruleId)}/captures`,
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
      const response = await reconRequest<AlertsResponse>(
        "GET",
        "/alerts",
        { query: { pageSize: 1000, cursor }, signal }
      )
      return response.cursor
    })
  },
  async getAlert(id: string, signal?: AbortSignal) {
    const response = await reconRequest<AlertResponse>(
      "GET",
      `/alerts/${encodeURIComponent(id)}`,
      { signal }
    )
    return response.data
  },
  async ackAlert(id: string, body: AckAlertRequest) {
    const response = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/ack`,
      { body }
    )
    return response.data
  },
  async resolveAlert(id: string, body: ResolveAlertRequest) {
    const response = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/resolve`,
      { body }
    )
    return response.data
  },
  async acceptAlert(id: string, body: AcceptAlertRequest) {
    const response = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/accept`,
      { body }
    )
    return response.data
  },
  async snoozeAlert(id: string, body: SnoozeAlertRequest) {
    const response = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/snooze`,
      { body }
    )
    return response.data
  },
  async unsnoozeAlert(id: string, body: UnsnoozeAlertRequest) {
    const response = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/unsnooze`,
      { body }
    )
    return response.data
  },
}

export type ReconClientV2 = typeof reconClientV2
