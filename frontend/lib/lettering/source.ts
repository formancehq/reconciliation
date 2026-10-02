/**
 * Where the Results tab reads lettering result files from: the one seam between
 * the files and DuckDB.
 *
 * A source lists a rule's files by their path in the results layout
 * (`rule={id}/day={YYYY-MM-DD}/run={runId}/{name}`, results doc §2) with a URL
 * to fetch each one. DuckDB sees the files under those paths, so the SQL of
 * tools/lettering-duckdb runs on them unchanged.
 *
 * `letteringSource` is the source the tab reads, and the only place to change
 * it. Today it is the tool's test data, served by a dev-only route. When
 * recon's API lists a rule's runs and each run's files with pre-signed URLs
 * (feature inventory E20), a source built on that listing replaces it here.
 */

/** One result file: its path in the rule=/day=/run= layout and where to fetch it. */
export interface LetteringFile {
  path: string
  url: string
}

export interface LetteringSource {
  /** Where the files come from, shown next to them. */
  label: string
  /** Test data, flagged as such in the tab so nobody mistakes it for a live rule. */
  testData: boolean
  /** The rules the source holds. */
  rules(): Promise<string[]>
  /** Every file of one rule: each run's manifest and data files. */
  files(rule: string): Promise<LetteringFile[]>
}

const TESTDATA_ROUTE = "/api/lettering/testdata"

async function getJson<T>(url: string): Promise<T> {
  const res = await fetch(url)
  if (res.status === 404)
    throw new Error("The lettering test data is served by the dev server only (pnpm dev).")
  if (!res.ok) throw new Error(`Listing the test data failed: HTTP ${res.status}`)
  return (await res.json()) as T
}

/** tools/lettering-duckdb/testdata, through app/api/lettering/testdata (dev server only). */
const testdataSource: LetteringSource = {
  label: "tools/lettering-duckdb/testdata",
  testData: true,
  rules: async () => (await getJson<{ rules: string[] }>(TESTDATA_ROUTE)).rules,
  files: async (rule) =>
    (await getJson<{ files: LetteringFile[] }>(`${TESTDATA_ROUTE}?rule=${encodeURIComponent(rule)}`)).files,
}

/** The source the Results tab reads, or null when there is none and the tab is hidden. */
export const letteringSource: LetteringSource | null =
  process.env.NODE_ENV === "development" ? testdataSource : null

/** Whether the Results tab, and the routes it needs, are on. */
export const LETTERING_SOURCE_AVAILABLE = letteringSource !== null
