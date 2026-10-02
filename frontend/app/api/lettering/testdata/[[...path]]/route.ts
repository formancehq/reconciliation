/**
 * Dev-only, read-only access to tools/lettering-duckdb/testdata, the lettering
 * result files the Results tab reads (lib/lettering/source.ts).
 *
 *   GET /api/lettering/testdata          the index: every file under rule=…
 *   GET /api/lettering/testdata/<path>   one file's bytes
 *
 * Outside `next dev` every request is a 404, and nothing reads the directory, so
 * a production build neither serves nor ships the test data.
 */
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'

export const runtime = 'nodejs'
export const dynamic = 'force-dynamic'

const ROOT = path.resolve(process.cwd(), '..', 'tools', 'lettering-duckdb', 'testdata')
const ROUTE = '/api/lettering/testdata'

/** The result files: everything under a rule=… directory (not expected/ nor generate.py). */
async function listFiles(dir: string): Promise<string[]> {
	const entries = await readdir(dir, { withFileTypes: true })
	const nested = await Promise.all(
		entries.map((e) => (e.isDirectory() ? listFiles(path.join(dir, e.name)) : [path.join(dir, e.name)])),
	)
	return nested.flat()
}

export async function GET(_req: Request, ctx: { params: Promise<{ path?: string[] }> }): Promise<Response> {
	if (process.env.NODE_ENV !== 'development') return new Response(null, { status: 404 })

	const segments = (await ctx.params).path ?? []
	if (segments.length === 0) {
		const rules = (await readdir(ROOT).catch(() => [] as string[])).filter((name) => name.startsWith('rule='))
		const files = (await Promise.all(rules.map((rule) => listFiles(path.join(ROOT, rule))))).flat().sort()
		return Response.json({
			files: files.map((file) => {
				const rel = path.relative(ROOT, file).split(path.sep).join('/')
				return { path: rel, url: `${ROUTE}/${rel.split('/').map(encodeURIComponent).join('/')}` }
			}),
		})
	}

	// Checked on the resolved path, so an encoded `..` cannot leave rule=… for expected/ or generate.py.
	const file = path.resolve(ROOT, ...segments)
	const rel = path.relative(ROOT, file)
	if (rel.startsWith('..') || path.isAbsolute(rel) || !rel.split(path.sep)[0].startsWith('rule=')) {
		return new Response(null, { status: 404 })
	}
	try {
		const bytes = await readFile(file)
		return new Response(new Uint8Array(bytes), {
			headers: { 'Content-Type': 'application/octet-stream', 'Cache-Control': 'no-store' },
		})
	} catch {
		return new Response(null, { status: 404 })
	}
}
