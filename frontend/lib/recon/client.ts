/**
 * Typed client for the Reconciliation ("Ledger Clarity") REST API.
 *
 * Talks to the same-origin proxy at `/api/recon/*` (see
 * app/api/recon/[[...path]]/route.ts), which forwards to `RECON_API_URL`. This
 * keeps CORS + upstream config server-side.
 *
 * Envelope handling (per recon-api/RECON-API.md):
 *   - single object   → `{ data: T }`      → returns `T`
 *   - list            → `{ cursor: {...} }` → returns `T[]` (recon sends
 *                        `data: null` for an empty page; normalized to `[]`)
 *   - any non-2xx     → `{ errorCode, errorMessage, details }` → throws ReconError
 */
import { getReconEndpoint } from "./endpoint"
import { collectReconPages } from "./pagination"
import type {
  AckAlertRequest,
  AcceptAlertRequest,
  Alert,
  AlertResponse,
  AlertsResponse,
  SigningKey,
  SigningKeysResponse,
  AuditEntry,
  AuditEntriesResponse,
  Capture,
  CapturesResponse,
  Cursor,
  Evaluation,
  EvaluateRuleRequest,
  EvaluationResponse,
  ResolveAlertRequest,
  Rule,
  RuleActivitiesResponse,
  RuleActivity,
  RulePatchRequest,
  RuleRequest,
  RuleResponse,
  RulesResponse,
  SnoozeAlertRequest,
  UnsnoozeAlertRequest,
} from "./types"

const BASE = "/api/recon"

/** A recon API error carrying the server's error envelope + HTTP status. */
export class ReconError extends Error {
  readonly errorCode: string
  readonly details?: string
  readonly status: number

  constructor(
    status: number,
    errorCode: string,
    errorMessage: string,
    details?: string
  ) {
    super(errorMessage || `Reconciliation request failed (${status})`)
    this.name = "ReconError"
    this.status = status
    this.errorCode = errorCode
    this.details = details
  }

  /** True when the recon server (or its proxy) could not be reached at all. */
  get isUnreachable(): boolean {
    return this.status === 502 || this.status === 0
  }
}

export type ReconQuery = Record<
  string,
  string | number | boolean | undefined | null
>

function buildPath(path: string, query?: ReconQuery): string {
  if (!query) return `${BASE}${path}`
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== null && v !== "") qs.set(k, String(v))
  }
  const s = qs.toString()
  return s ? `${BASE}${path}?${s}` : `${BASE}${path}`
}

export async function reconRequest<T>(
  method: string,
  path: string,
  opts?: { body?: unknown; query?: ReconQuery; signal?: AbortSignal }
): Promise<T> {
  // Per-request headers, incl. the optional recon endpoint override + token
  // (see lib/recon/endpoint.ts; the proxy validates X-Recon-Url via its SSRF guard).
  const headers: Record<string, string> = {}
  if (opts?.body !== undefined) headers["Content-Type"] = "application/json"
  const ep = getReconEndpoint()
  if (ep.url) headers["X-Recon-Url"] = ep.url
  if (ep.token) headers["Authorization"] = `Bearer ${ep.token}`

  let res: Response
  try {
    res = await fetch(buildPath(path, opts?.query), {
      method,
      headers,
      body: opts?.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: opts?.signal,
    })
  } catch (err) {
    if (
      opts?.signal?.aborted ||
      (err instanceof Error && err.name === "AbortError")
    )
      throw err
    // Network-level failure (offline, proxy crash): surface as unreachable.
    throw new ReconError(
      0,
      "NETWORK",
      err instanceof Error ? err.message : "Network error"
    )
  }

  if (res.status === 204) return undefined as T

  const text = await res.text()
  const json = text ? safeParse(text) : undefined

  if (!res.ok) {
    const env = (json ?? {}) as Partial<{
      errorCode: string
      errorMessage: string
      details: string
    }>
    throw new ReconError(
      res.status,
      env.errorCode ?? "UNKNOWN",
      env.errorMessage ?? `Request failed (${res.status})`,
      env.details
    )
  }
  return json as T
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

/** Normalize a cursor page (recon sends `data: null` when empty). */
export function cursorItems<T>(cursor: Cursor<T>): T[] {
  return cursor.data ?? []
}

export const reconClient = {
  // --- health -------------------------------------------------------------
  async health(signal?: AbortSignal): Promise<boolean> {
    try {
      await reconRequest<unknown>("GET", "/_healthcheck", { signal })
      return true
    } catch {
      return false
    }
  },

  // --- rules --------------------------------------------------------------
  async listRules(signal?: AbortSignal): Promise<Rule[]> {
    return collectReconPages(async (cursor) => {
      const response = await reconRequest<RulesResponse>("GET", "/rules", {
        query: { pageSize: 1000, cursor },
        signal,
      })
      return response.cursor
    })
  },
  async getRule(id: string, signal?: AbortSignal): Promise<Rule> {
    const r = await reconRequest<RuleResponse>(
      "GET",
      `/rules/${encodeURIComponent(id)}`,
      { signal }
    )
    return r.data
  },
  async createRule(body: RuleRequest): Promise<Rule> {
    const r = await reconRequest<RuleResponse>("POST", "/rules", { body })
    return r.data
  },
  async patchRule(id: string, body: RulePatchRequest): Promise<Rule> {
    const r = await reconRequest<RuleResponse>(
      "PATCH",
      `/rules/${encodeURIComponent(id)}`,
      { body }
    )
    return r.data
  },
  async deleteRule(id: string): Promise<void> {
    await reconRequest<void>("DELETE", `/rules/${encodeURIComponent(id)}`)
  },
  async evaluateRule(
    id: string,
    body?: EvaluateRuleRequest
  ): Promise<Evaluation> {
    const r = await reconRequest<EvaluationResponse>(
      "POST",
      `/rules/${encodeURIComponent(id)}/evaluate`,
      { body: body ?? {} }
    )
    return r.data
  },
  async listRuleTimeline(
    ruleId: string,
    cursor?: string,
    signal?: AbortSignal
  ): Promise<Cursor<RuleActivity>> {
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

  // --- captures (evaluation receipts; the rule timeline is canonical) -----
  async listCaptures(
    ruleId: string,
    opts?: { period?: string; signal?: AbortSignal }
  ): Promise<Capture[]> {
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

  // --- alerts -------------------------------------------------------------
  async listAlerts(signal?: AbortSignal): Promise<Alert[]> {
    return collectReconPages(async (cursor) => {
      const response = await reconRequest<AlertsResponse>("GET", "/alerts", {
        query: { pageSize: 1000, cursor },
        signal,
      })
      return response.cursor
    })
  },
  async getAlert(id: string, signal?: AbortSignal): Promise<Alert> {
    const r = await reconRequest<AlertResponse>(
      "GET",
      `/alerts/${encodeURIComponent(id)}`,
      { signal }
    )
    return r.data
  },
  async ackAlert(id: string, body: AckAlertRequest): Promise<Alert> {
    const r = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/ack`,
      { body }
    )
    return r.data
  },
  async resolveAlert(id: string, body: ResolveAlertRequest): Promise<Alert> {
    const r = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/resolve`,
      { body }
    )
    return r.data
  },
  async acceptAlert(id: string, body: AcceptAlertRequest): Promise<Alert> {
    const r = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/accept`,
      { body }
    )
    return r.data
  },
  async snoozeAlert(id: string, body: SnoozeAlertRequest): Promise<Alert> {
    const r = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/snooze`,
      { body }
    )
    return r.data
  },
  async unsnoozeAlert(id: string, body: UnsnoozeAlertRequest): Promise<Alert> {
    const r = await reconRequest<AlertResponse>(
      "POST",
      `/alerts/${encodeURIComponent(id)}/unsnooze`,
      { body }
    )
    return r.data
  },

  // --- audit --------------------------------------------------------------
  async getSigningKeys(signal?: AbortSignal): Promise<SigningKey[]> {
    const r = await reconRequest<SigningKeysResponse>(
      "GET",
      "/audit/signing-keys",
      { signal }
    )
    return r.data.keys ?? []
  },
  async getAuditEntries(limit = 50, signal?: AbortSignal): Promise<AuditEntry[]> {
    const r = await reconRequest<AuditEntriesResponse>(
      "GET",
      "/audit/entries",
      { query: { limit }, signal }
    )
    return r.data.entries ?? []
  },
}

export type ReconClient = typeof reconClient
