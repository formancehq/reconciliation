/**
 * Serves DuckDB-WASM's single-threaded bundles (mvp and eh: a wasm module and its
 * worker) from the installed @duckdb/duckdb-wasm package, so the Results tab
 * needs no CDN and no copy step, and the bytes always match the package version.
 *
 * The threaded `coi` bundle is left out on purpose: it needs SharedArrayBuffer,
 * so a cross-origin-isolated page, which the console's iframe embedding is not.
 * The URL carries the package version (`?v=`), so the files cache for good.
 */
import { readFile } from 'node:fs/promises'
import path from 'node:path'

export const runtime = 'nodejs'

const FILES: Record<string, string> = {
	'duckdb-mvp.wasm': 'application/wasm',
	'duckdb-eh.wasm': 'application/wasm',
	'duckdb-browser-mvp.worker.js': 'text/javascript; charset=utf-8',
	'duckdb-browser-eh.worker.js': 'text/javascript; charset=utf-8',
}

const DIST = path.resolve(process.cwd(), 'node_modules', '@duckdb', 'duckdb-wasm', 'dist')

export async function GET(_req: Request, ctx: { params: Promise<{ file: string }> }): Promise<Response> {
	const { file } = await ctx.params
	const type = FILES[file]
	if (!type) return new Response(null, { status: 404 })
	const bytes = await readFile(path.join(DIST, file))
	return new Response(new Uint8Array(bytes), {
		headers: { 'Content-Type': type, 'Cache-Control': 'public, max-age=31536000, immutable' },
	})
}
