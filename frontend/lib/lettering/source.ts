/**
 * Where the Results tab reads lettering result files from: the one seam between
 * the files and DuckDB.
 *
 * A source lists files by their path in the results layout
 * (`rule={id}/day={YYYY-MM-DD}/run={runId}/{name}`, results doc §2) with a URL
 * to fetch each one. DuckDB sees the files under those paths, so the SQL of
 * tools/lettering-duckdb runs on them unchanged.
 *
 * Today the only source is the tool's test data, served by a dev-only route.
 * When recon's API lists a run's files with pre-signed URLs (feature inventory
 * E20), a second source maps that listing to the same shape: nothing else
 * changes.
 */

/** One result file: its path in the rule=/day=/run= layout and where to fetch it. */
export interface LetteringFile {
  path: string
  url: string
}

export interface LetteringSource {
  /** Shown next to the data, so nobody mistakes test data for a live rule. */
  label: string
  /** Every file of every rule the source holds. */
  list(): Promise<LetteringFile[]>
}

/**
 * Whether the Results tab has a source to read, which gates the tab. The test
 * data is served under `next dev` only; recon's API will be the source later.
 */
export const LETTERING_SOURCE_AVAILABLE = process.env.NODE_ENV === "development"

/** tools/lettering-duckdb/testdata, through app/api/lettering/testdata (dev server only). */
export const testdataSource: LetteringSource = {
  label: "tools/lettering-duckdb/testdata",
  async list() {
    const res = await fetch("/api/lettering/testdata")
    if (res.status === 404)
      throw new Error(
        "The lettering test data is served by the dev server only (pnpm dev)."
      )
    if (!res.ok) throw new Error(`Listing the test data failed: HTTP ${res.status}`)
    const body = (await res.json()) as { files: LetteringFile[] }
    return body.files
  },
}
