/**
 * Serves DuckDB-WASM's single-threaded bundles (mvp and eh: a wasm module and its
 * worker) from the installed @duckdb/duckdb-wasm package, so the Results tab
 * needs no CDN and no copy step, and the bytes always match the package version.
 *
 * The threaded `coi` bundle is left out on purpose: it needs SharedArrayBuffer,
 * so a cross-origin-isolated page, which the console's iframe embedding is not.
 * The URL carries the package version (`?v=`), so the files cache for good.
 *
 * It answers only while the Results tab has a source (lib/lettering/source.ts):
 * under `next dev` today, so a production build serves nothing.
 */
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { LETTERING_SOURCE_AVAILABLE } from '@/lib/lettering/source'

export const runtime = 'nodejs'

// File name → content type. A Map, so a name such as `constructor` is not found on a prototype.
const FILES = new Map([
	['duckdb-mvp.wasm', 'application/wasm'],
	['duckdb-eh.wasm', 'application/wasm'],
	['duckdb-browser-mvp.worker.js', 'text/javascript; charset=utf-8'],
	['duckdb-browser-eh.worker.js', 'text/javascript; charset=utf-8'],
])

const DIST = path.resolve(process.cwd(), 'node_modules', '@duckdb', 'duckdb-wasm', 'dist')

export async function GET(_req: Request, ctx: { params: Promise<{ file: string }> }): Promise<Response> {
	if (!LETTERING_SOURCE_AVAILABLE) return new Response(null, { status: 404 })
	const { file } = await ctx.params
	const type = FILES.get(file)
	if (!type) return new Response(null, { status: 404 })
	try {
		const bytes = await readFile(path.join(DIST, file))
		return new Response(new Uint8Array(bytes), {
			headers: { 'Content-Type': type, 'Cache-Control': 'public, max-age=31536000, immutable' },
		})
	} catch {
		return new Response(null, { status: 404 })
	}
}
