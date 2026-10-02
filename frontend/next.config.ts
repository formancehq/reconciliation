import type { NextConfig } from 'next'

// The module owns and serves its own UI; the console shell (draft-formance-poc)
// discovers a running module via /_info and EMBEDS this UI in an iframe. So we
// must NOT send X-Frame-Options: DENY and we relax frame-ancestors to localhost
// dev origins. Tighten frame-ancestors to the real console origin in production.
const FRAME_ANCESTORS = process.env.RECONCILIATION_FRAME_ANCESTORS || "'self' http://localhost:* http://127.0.0.1:*"

const nextConfig: NextConfig = {
	eslint: {
		ignoreDuringBuilds: true,
	},
	// DuckDB-WASM runs in the browser only (lib/lettering/duckdb.ts imports it on
	// demand): keep the server build from bundling its Node entry point.
	serverExternalPackages: ['@duckdb/duckdb-wasm'],
	// The Results tab runs the SQL of ../tools/lettering-duckdb as it is: each .sql
	// file is bundled as a string at build time (lib/lettering/sql.ts), so the tab
	// and the tool never drift.
	webpack(config) {
		config.module.rules.push({ test: /\.sql$/, type: 'asset/source' })
		return config
	},
	async headers() {
		return [
			{
				source: '/:path*',
				headers: [{ key: 'Content-Security-Policy', value: `frame-ancestors ${FRAME_ANCESTORS};` }],
			},
		]
	},
}

export default nextConfig
