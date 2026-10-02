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
import { QUERIES, SQL } from "./sql"

/** The rules a source holds, from the `rule=` segment of its paths. */
export function rulesOf(files: LetteringFile[]): string[] {
  return [...new Set(files.map((f) => f.path.split("/")[0].replace(/^rule=/, "")))].sort()
}

/** The files of one rule. */
export function filesOf(files: LetteringFile[], rule: string): LetteringFile[] {
  return files.filter((f) => f.path.startsWith(`rule=${rule}/`))
}

export interface RuleRun {
  day: string
  run: string
  verdict: string
  /** The day's current run: its latest complete run (rule.sql `current_runs`). */
  current: boolean
  /** Why an incomplete run concluded nothing. */
  reason: string | null
}

/** Every run of a rule, through the tool's `current-runs` query. */
export async function readRuleRuns(files: LetteringFile[], rule: string): Promise<RuleRun[]> {
  await loadFiles(filesOf(files, rule))
  const query = QUERIES.find((q) => q.name === "current-runs")
  if (!query) throw new Error("tools/lettering-duckdb/queries/current-runs.sql is missing")
  const result = await withSession({ rule: letteringDir(`rule=${rule}`) }, async (s) => {
    await s.exec(SQL.schema)
    await s.exec(SQL.rule)
    return s.query(query.sql)
  })
  return result.rows as unknown as RuleRun[]
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
