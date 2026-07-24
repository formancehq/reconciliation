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
