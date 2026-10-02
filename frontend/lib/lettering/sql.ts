/**
 * The SQL of tools/lettering-duckdb, bundled as it is (the asset/source rule in
 * next.config.ts). The tool is the single source of truth: this module only
 * loads its files and reads the header each query declares, the same header
 * the `lettering` wrapper reads.
 */
import schemaSql from "../../../tools/lettering-duckdb/sql/schema.sql"
import runSql from "../../../tools/lettering-duckdb/sql/run.sql"
import ruleSql from "../../../tools/lettering-duckdb/sql/rule.sql"
import checkSql from "../../../tools/lettering-duckdb/check.sql"
import checkChainSql from "../../../tools/lettering-duckdb/check-chain.sql"

export const SQL = {
  schema: schemaSql,
  run: runSql,
  rule: ruleSql,
  check: checkSql,
  checkChain: checkChainSql,
}

export interface LetteringQuery {
  /** The file name without .sql: `open-breaks`. */
  name: string
  /** The question on the file's first line. */
  question: string
  /** Its variables besides `rule`, from the `-- Variables:` line. */
  variables: { name: string; required: boolean }[]
  sql: string
}

/**
 * Reads a query's header like the wrapper does:
 *   -- What must be done today? The open breaks, most urgent first.
 *   -- Variables: rule, day (optional, default the latest day).
 */
function parseQuery(name: string, sql: string): LetteringQuery {
  const lines = sql.split("\n")
  const question = (lines[0] ?? "").replace(/^--\s*/, "")
  const header =
    lines.find((l) => l.startsWith("-- Variables:"))?.replace(/^-- Variables:\s*/, "") ?? ""
  const required = new Set(
    [...header.matchAll(/([A-Za-z_][A-Za-z0-9_]*) *\(required/g)].map((m) => m[1])
  )
  const variables = header
    .replace(/\([^)]*\)/g, "")
    .replace(/\.$/, "")
    .split(",")
    .map((part) => part.trim())
    .filter((v) => v && v !== "rule")
    .map((v) => ({ name: v, required: required.has(v) }))
  return { name, question, variables, sql }
}

const queries = require.context("../../../tools/lettering-duckdb/queries", false, /\.sql$/)

/** Every query of tools/lettering-duckdb/queries, by file name. */
export const QUERIES: LetteringQuery[] = queries
  .keys()
  .filter((key) => key.startsWith("./"))
  .map((key) => parseQuery(key.slice(2, -4), queries(key)))
  .sort((a, b) => a.name.localeCompare(b.name))
