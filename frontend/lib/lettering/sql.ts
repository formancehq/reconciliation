/**
 * The SQL of tools/lettering-duckdb, bundled as it is (the asset/source rule in
 * next.config.ts). The tool is the single source of truth: this module only
 * loads its files and reads the header each query declares, the same header
 * the `lettering` wrapper reads.
 */
import shapeSql from "../../../tools/lettering-duckdb/sql/shape.sql"
import schemaSql from "../../../tools/lettering-duckdb/sql/schema.sql"
import runSql from "../../../tools/lettering-duckdb/sql/run.sql"
import ruleSql from "../../../tools/lettering-duckdb/sql/rule.sql"
import checkSql from "../../../tools/lettering-duckdb/check.sql"
import checkChainSql from "../../../tools/lettering-duckdb/check-chain.sql"

export const SQL = {
  shape: shapeSql,
  schema: schemaSql,
  run: runSql,
  rule: ruleSql,
  check: checkSql,
  checkChain: checkChainSql,
}

export interface LetteringQuery {
  /** The file name without .sql: `open-breaks`. */
  name: string
  /** The question: the header's first sentence. */
  question: string
  /** The rest of the header's description, before `-- Variables:`. */
  detail: string
  /** Its variables besides `rule`, from the `-- Variables:` line. */
  variables: QueryVariable[]
  sql: string
}

export interface QueryVariable {
  name: string
  /** Marked `(required…`, as the wrapper reads it. */
  required: boolean
  /** The text in parentheses: `optional, default the latest day`. */
  hint?: string
}

/**
 * Reads a query's header, the lines the wrapper reads:
 *   -- What must be done today? The open breaks, most urgent first.
 *   -- Variables: rule, day (optional, default the latest day).
 * The description may run over several lines, up to `-- Variables:`.
 */
function parseQuery(name: string, sql: string): LetteringQuery {
  const lines = sql.split("\n")
  const at = lines.findIndex((l) => l.startsWith("-- Variables:"))
  const description = lines
    .slice(0, Math.max(at, 1))
    .map((l) => l.replace(/^--\s*/, ""))
    .join(" ")
    .trim()
  const end = description.search(/[.?](\s|$)/)
  const question = end < 0 ? description : description.slice(0, end + 1)
  const detail = description.slice(question.length).trim()
  const header = at < 0 ? "" : lines[at].replace(/^-- Variables:\s*/, "")
  // `name (text)` pairs: the parentheses may hold commas, so they are matched whole.
  const variables = [...header.matchAll(/([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(([^)]*)\))?/g)]
    .map((m) => ({ name: m[1], hint: m[2]?.trim(), required: /^required/.test(m[2] ?? "") }))
    .filter((v) => v.name !== "rule")
  return { name, question, detail, variables, sql }
}

const queries = require.context("../../../tools/lettering-duckdb/queries", false, /\.sql$/)

/** Every query of tools/lettering-duckdb/queries, by file name. */
export const QUERIES: LetteringQuery[] = queries
  .keys()
  .filter((key) => key.startsWith("./"))
  .map((key) => parseQuery(key.slice(2, -4), queries(key)))
  .sort((a, b) => a.name.localeCompare(b.name))
