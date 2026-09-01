/**
 * Server-side reverse proxy for the Reconciliation ("Ledger Clarity") REST API.
 *
 * The browser talks to `/api/recon/<...>` and this route forwards verbatim to
 * the recon server chosen by `RECON_API_URL` (default `http://localhost:8081`).
 * Same-origin proxying keeps CORS + the upstream base URL server-side, exactly
 * like the console's proxy this UI was extracted from.
 *
 * A caller may switch the target at runtime with the `X-Recon-Url` header (used
 * by the in-app endpoint switcher to point at a different recon instance). A
 * bearer token, if the app ever sets one, is forwarded as Authorization.
 */
export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

const ROUTE_PREFIX = '/api/recon';

const reconBaseUrl = (req: Request): string => {
  const override = (req.headers.get('x-recon-url') || '').trim();
  const base = override || process.env.RECON_API_URL || 'http://localhost:8081';
  return base.replace(/\/+$/, '');
};

async function proxy(req: Request): Promise<Response> {
  const url = new URL(req.url);
  const idx = url.pathname.indexOf(ROUTE_PREFIX);
  const downstreamPath = idx >= 0 ? url.pathname.slice(idx + ROUTE_PREFIX.length) : url.pathname;
  const targetUrl = `${reconBaseUrl(req)}${downstreamPath || '/'}${url.search}`;

  const headers: Record<string, string> = { Accept: 'application/json' };
  const ct = req.headers.get('content-type');
  if (ct) headers['Content-Type'] = ct;
  const auth = req.headers.get('authorization');
  if (auth) headers['Authorization'] = auth;

  const init: RequestInit = {
    method: req.method,
    headers,
    // Never auto-follow a 30x to a possibly-internal address.
    redirect: 'manual',
    signal: req.signal,
  };
  if (req.method !== 'GET' && req.method !== 'HEAD') {
    const body = await req.text();
    if (body) init.body = body;
  }

  let upstream: Response;
  try {
    upstream = await fetch(targetUrl, init);
  } catch {
    return Response.json(
      {
        errorCode: 'BAD_GATEWAY',
        errorMessage: 'Reconciliation server is unreachable',
        details: 'Is `go run . serve` running on the RECON_API_URL host (default http://localhost:8081)?',
      },
      { status: 502 },
    );
  }

  if (upstream.status >= 300 && upstream.status < 400) {
    return Response.json(
      { errorCode: 'BAD_GATEWAY', errorMessage: 'Upstream redirect is not allowed' },
      { status: 502 },
    );
  }

  // Pass the upstream body + status through, forcing a JSON content type
  // (the recon API is JSON-only, and 204s carry no body).
  if (upstream.status === 204) return new Response(null, { status: 204 });
  const text = await upstream.text();
  return new Response(text, {
    status: upstream.status,
    headers: { 'Content-Type': 'application/json' },
  });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const DELETE = proxy;
export const PATCH = proxy;
export const HEAD = proxy;
