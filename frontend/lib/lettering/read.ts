/**
 * What the Results tab reads, each through the SQL of tools/lettering-duckdb.
 *
 * The tool's files do the work: schema.sql and run.sql or rule.sql set up the
 * views, and the query files answer. The few statements written here only
 * select from those views for display; none recomputes a figure.
 */
import { letteringDir, loadFiles, withSession, type Rows } from "./duckdb"
import type { Manifest } from "./manifest"
import type { LetteringFile } from "./source"
import { QUERIES, SQL, type LetteringQuery } from "./sql"

export interface RuleRun {
  day: string
  run: string
  verdict: string
  /** The day's current run: its latest complete run (rule.sql `current_runs`). */
  current: boolean
  /** Why an incomplete run concluded nothing. */
  reason: string | null
  /** The run its manifest's `previousRun` names; absent on a first run. */
  previousDay?: string | null
  previousRun?: string | null
}

/**
 * Every run of a rule, through the tool's `current-runs` query, with the run each one chains
 * onto. `files` are the rule's files, from its LetteringSource.
 */
export async function readRuleRuns(files: LetteringFile[], rule: string): Promise<RuleRun[]> {
  await loadFiles(files)
  const query = QUERIES.find((q) => q.name === "current-runs")
  if (!query) throw new Error("tools/lettering-duckdb/queries/current-runs.sql is missing")
  return withSession({ rule: letteringDir(`rule=${rule}`) }, async (s) => {
    await s.exec(SQL.schema)
    await s.exec(SQL.rule)
    const runs = (await s.query(query.sql)).rows as unknown as RuleRun[]
    const links = await s.query(
      "SELECT run, m->'previousRun'->>'day' AS previous_day, m->'previousRun'->>'runId' AS previous_run FROM runs"
    )
    const byRun = new Map(links.rows.map((l) => [l.run, l]))
    return runs.map((r) => ({
      ...r,
      previousDay: byRun.get(r.run)?.previous_day as string | null,
      previousRun: byRun.get(r.run)?.previous_run as string | null,
    }))
  })
}

/** A run's path in the layout: `rule=…/day=…/run=…`. */
export function runPath(rule: string, run: { day: string; run: string }): string {
  return `rule=${rule}/day=${run.day}/run=${run.run}`
}

export interface Violation {
  rule: string
  key: string
  detail: string
}

export interface CheckResult {
  /** sound: no rule broken; violated: see `violations`; incomplete: nothing else to check;
   *  refused: the wrapper would refuse the arguments (exit 2). */
  status: "sound" | "violated" | "incomplete" | "refused"
  /** What the wrapper prints for the outcome. */
  message: string
  violations: Violation[]
}

const VIOLATED = /violation\(s\)/

/**
 * `lettering check <run>`: the run's shape (sql/shape.sql), then check.sql on a complete run.
 * check.sql raises an error on a violation, after filling its `violations` temp table, which
 * is read on the same connection.
 */
export async function checkRun(path: string): Promise<CheckResult> {
  return withSession({ run: letteringDir(path) }, async (s) => {
    await s.exec(SQL.shape)
    const shape = await s.query("SELECT rule, key, detail FROM shape_violations(getvariable('run'))")
    if (shape.rows.length > 0) return violated(shape, "the run's shape breaks the rules below")
    const state = (await s.query("SELECT verdict, reason FROM run_shape(getvariable('run'))")).rows[0]
    if (state.verdict === "incomplete") {
      return {
        status: "incomplete",
        message: `incomplete run (${state.reason ?? "-"}): it writes no data file, so there is nothing else to check`,
        violations: [],
      }
    }
    await s.exec(SQL.schema)
    await s.exec(SQL.run)
    try {
      await s.exec(SQL.check)
      return { status: "sound", message: "ok: the run is sound", violations: [] }
    } catch (e) {
      if (!VIOLATED.test(String(e))) throw e
      return violated(await s.query("SELECT rule, key, detail FROM violations ORDER BY rule, key"), errorText(e))
    }
  })
}

/**
 * `lettering check-chain <earlier> <run>`: refuses an incomplete later run, checks that the
 * earlier run is a link (sql/shape.sql), then runs check-chain.sql.
 */
export async function checkChain(prevPath: string, path: string): Promise<CheckResult> {
  return withSession({ prev: letteringDir(prevPath), run: letteringDir(path) }, async (s) => {
    await s.exec(SQL.shape)
    const later = (await s.query("SELECT verdict FROM run_shape(getvariable('run'))")).rows[0]
    if (later.verdict === "incomplete") {
      return {
        status: "refused",
        message: `${letteringDir(path)} is an incomplete run: it is not a link in the chain, so there is nothing to check`,
        violations: [],
      }
    }
    const link = await s.query("SELECT rule, key, detail FROM chain_shape_violations(getvariable('prev'))")
    if (link.rows.length > 0) return violated(link, "the earlier run is not a link in the chain")
    await s.exec(SQL.schema)
    await s.exec(SQL.run)
    try {
      await s.exec(SQL.checkChain)
      return { status: "sound", message: "ok: the run chains onto the earlier one", violations: [] }
    } catch (e) {
      if (!VIOLATED.test(String(e))) throw e
      return violated(await s.query("SELECT rule, key, detail FROM chain_violations ORDER BY rule, key"), errorText(e))
    }
  })
}

function violated(rows: Rows, message: string): CheckResult {
  return { status: "violated", message, violations: rows.rows as unknown as Violation[] }
}

/** A DuckDB error without its class: `Invalid Input Error: 2 violation(s)…` → `2 violation(s)…`. */
function errorText(e: unknown): string {
  return (e instanceof Error ? e.message : String(e)).replace(/^[A-Za-z ]*Error: /, "")
}

/**
 * One query of tools/lettering-duckdb/queries over every day of a rule (`files`: its files), as
 * `lettering query` runs it: the query's own variables only, set before
 * schema.sql and rule.sql, whose last statement refuses a day with no complete run.
 */
export async function readQuery(
  files: LetteringFile[],
  rule: string,
  query: LetteringQuery,
  vars: Record<string, string | undefined>
): Promise<Rows> {
  const missing = query.variables.find((v) => v.required && !vars[v.name])
  if (missing) throw new Error(`Query ${query.name} needs ${missing.name}.`)
  await loadFiles(files)
  const declared = Object.fromEntries(query.variables.map((v) => [v.name, vars[v.name] || undefined]))
  return withSession({ rule: letteringDir(`rule=${rule}`), ...declared }, async (s) => {
    await s.exec(SQL.schema)
    await s.exec(SQL.rule)
    return s.query(query.sql)
  })
}

export interface RunResults {
  manifest: Manifest
  /** breaks.ndjson.gz, in file order; absent on an incomplete run, which writes no data file. */
  breaks?: Rows
  /** Open holds of the wrong sign per book, from stock.ndjson.gz. */
  wrongSign?: { side: string; prefix: string; asset: string; holds: number }[]
}

/** One run, through run.sql: `rule=…/day=…/run=…`. */
export async function readRun(path: string): Promise<RunResults> {
  return withSession({ run: letteringDir(path) }, async (s) => {
    await s.exec(SQL.schema)
    await s.exec(SQL.run)
    const m = await s.query("SELECT m::VARCHAR AS m FROM manifest")
    const manifest = JSON.parse(String(m.rows[0].m)) as Manifest
    if (manifest.verdict === "incomplete") return { manifest }
    const breaks = await s.query("SELECT * FROM breaks ORDER BY pos")
    const wrongSign = await s.query(
      "SELECT side, prefix, asset, count(*) AS holds FROM stock WHERE class = 'wrong_sign' GROUP BY ALL"
    )
    return {
      manifest,
      breaks,
      wrongSign: wrongSign.rows as unknown as RunResults["wrongSign"],
    }
  })
}
