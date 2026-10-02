/**
 * DuckDB-WASM for the Results tab: one in-browser database, in a Web Worker, that
 * runs the SQL of tools/lettering-duckdb on files fetched from a LetteringSource.
 *
 * - Bundle: the single-threaded `eh` build (`mvp` as a fallback), served by
 *   app/duckdb/[file]. It needs no SharedArrayBuffer, so it runs inside the
 *   console's iframe without cross-origin isolation.
 * - Files: each one is fetched once and registered under `lettering/{path}`.
 *   DuckDB globs registered names, so the `day=*` and `run=*` globs of `rule.sql`
 *   work with no directory listing.
 * - Connections: one per call, closed after it, so the SQL variables (`run`,
 *   `rule`, `day`…) and the checks' temp tables never leak between calls.
 */
import type { AsyncDuckDB, AsyncDuckDBConnection } from "@duckdb/duckdb-wasm"
import type { LetteringFile } from "./source"

/** The prefix DuckDB sees the files under: `lettering/rule=…/day=…/run=…/flow.ndjson.gz`. */
const ROOT = "lettering"

export type Cell =
  | string
  | number
  | bigint
  | boolean
  | null
  | Cell[]
  | { [key: string]: Cell }

/**
 * A column's kind, from its DuckDB type. Amounts are exactly the HUGEINT columns:
 * the readers declare every amount HUGEINT (schema.sql), and nothing else.
 */
export type ColumnKind =
  | "amount"
  | "integer"
  | "number"
  | "date"
  | "timestamp"
  | "boolean"
  | "text"
  | "nested"

export interface Column {
  name: string
  kind: ColumnKind
}

export interface Rows {
  columns: Column[]
  rows: Record<string, Cell>[]
}

type Table = Awaited<ReturnType<AsyncDuckDBConnection["query"]>>
type ArrowType = Table["schema"]["fields"][number]["type"]

// apache-arrow's Type ids (stable across versions), for the types the files use.
// apache-arrow is a dependency of duckdb-wasm, not importable from this app.
const ARROW = {
  Int: 2,
  Float: 3,
  Bool: 6,
  Decimal: 7,
  Date: 8,
  Timestamp: 10,
  List: 12,
  Struct: 13,
} as const

function kindOf(type: ArrowType): ColumnKind {
  switch (type.typeId) {
    case ARROW.Decimal:
      return "amount"
    case ARROW.Int:
      return "integer"
    case ARROW.Float:
      return "number"
    case ARROW.Date:
      return "date"
    case ARROW.Timestamp:
      return "timestamp"
    case ARROW.Bool:
      return "boolean"
    case ARROW.List:
    case ARROW.Struct:
      return "nested"
    default:
      return "text"
  }
}

/** An Arrow value as plain JS: exact amounts as bigint, days as YYYY-MM-DD, instants as ISO UTC. */
function toCell(value: unknown, type: ArrowType): Cell {
  if (value === null || value === undefined) return null
  switch (type.typeId) {
    case ARROW.Decimal:
      return BigInt(String(value))
    case ARROW.Int:
      if (typeof value === "bigint")
        return value >= BigInt(Number.MIN_SAFE_INTEGER) && value <= BigInt(Number.MAX_SAFE_INTEGER)
          ? Number(value)
          : value
      return value as number
    case ARROW.Date:
      return new Date(value as number).toISOString().slice(0, 10)
    case ARROW.Timestamp:
      return new Date(value as number).toISOString().replace(".000Z", "Z")
    case ARROW.List: {
      const child = (type as unknown as { children: { type: ArrowType }[] }).children[0].type
      return Array.from(value as Iterable<unknown>, (v) => toCell(v, child))
    }
    case ARROW.Struct: {
      const fields = (type as unknown as { children: { name: string; type: ArrowType }[] }).children
      const row = value as Record<string, unknown>
      return Object.fromEntries(fields.map((f) => [f.name, toCell(row[f.name], f.type)]))
    }
    default:
      return typeof value === "boolean" || typeof value === "number" ? value : String(value)
  }
}

function toRows(table: Table): Rows {
  const fields = table.schema.fields
  const columns = fields.map((f) => ({ name: f.name, kind: kindOf(f.type) }))
  const rows: Record<string, Cell>[] = []
  for (let i = 0; i < table.numRows; i++) {
    const row = table.get(i) as Record<string, unknown> | null
    if (!row) continue
    rows.push(Object.fromEntries(fields.map((f) => [f.name, toCell(row[f.name], f.type)])))
  }
  return { columns, rows }
}

/** A SQL string literal, quoted as the wrapper quotes it. */
function quote(value: string): string {
  return `'${value.replace(/'/g, "''")}'`
}

/** The directory DuckDB sees for a path of the layout: `rule=…/day=…/run=…`. */
export function letteringDir(path: string): string {
  return `${ROOT}/${path.replace(/\/+$/, "")}`
}

/** One connection, with its SQL variables set: run SQL files on it, then read a result. */
export interface Session {
  /** Runs a file of statements (schema.sql, run.sql…); their results are dropped. */
  exec(sql: string): Promise<void>
  /** Runs ONE statement (a query file, a SELECT) and returns its rows. */
  query(sql: string): Promise<Rows>
}

let dbPromise: Promise<AsyncDuckDB> | undefined
const registered = new Map<string, Promise<void>>()

async function openDb(): Promise<AsyncDuckDB> {
  const duckdb = await import("@duckdb/duckdb-wasm")
  const asset = (file: string) =>
    new URL(`/duckdb/${file}?v=${duckdb.PACKAGE_VERSION}`, window.location.origin).href
  const bundle = await duckdb.selectBundle({
    mvp: { mainModule: asset("duckdb-mvp.wasm"), mainWorker: asset("duckdb-browser-mvp.worker.js") },
    eh: { mainModule: asset("duckdb-eh.wasm"), mainWorker: asset("duckdb-browser-eh.worker.js") },
  })
  const worker = new Worker(bundle.mainWorker!)
  const db = new duckdb.AsyncDuckDB(new duckdb.VoidLogger(), worker)
  await db.instantiate(bundle.mainModule)
  await db.open({ query: { castBigIntToDouble: false, castDecimalToDouble: false } })
  return db
}

export function getDb(): Promise<AsyncDuckDB> {
  dbPromise ??= openDb().catch((e) => {
    dbPromise = undefined
    throw e
  })
  return dbPromise
}

/** Fetches the files not registered yet and registers them under `lettering/{path}`. */
export async function loadFiles(files: LetteringFile[]): Promise<void> {
  const db = await getDb()
  await Promise.all(
    files.map((file) => {
      let done = registered.get(file.path)
      if (!done) {
        done = (async () => {
          const res = await fetch(file.url)
          if (!res.ok) throw new Error(`Fetching ${file.path} failed: HTTP ${res.status}`)
          await db.registerFileBuffer(`${ROOT}/${file.path}`, new Uint8Array(await res.arrayBuffer()))
        })().catch((e) => {
          registered.delete(file.path)
          throw e
        })
        registered.set(file.path, done)
      }
      return done
    })
  )
}

/**
 * Opens a connection, sets the variables (SET VARIABLE, as the wrapper does),
 * hands it to `fn`, and closes it.
 */
export async function withSession<T>(
  vars: Record<string, string | undefined>,
  fn: (session: Session) => Promise<T>
): Promise<T> {
  const db = await getDb()
  const conn = await db.connect()
  try {
    for (const [name, value] of Object.entries(vars)) {
      if (value !== undefined) await conn.query(`SET VARIABLE ${name} = ${quote(value)}`)
    }
    return await fn({
      exec: async (sql) => {
        await conn.query(sql)
      },
      query: async (sql) => toRows(await conn.query(sql)),
    })
  } finally {
    await conn.close()
  }
}
