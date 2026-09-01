/**
 * Client-side selection of which recon backend the UI talks to.
 *
 * The proxy (`/api/recon`) forwards to `RECON_API_URL` by default, but the
 * browser can override the target per-request with an `X-Recon-Url` header (and
 * an optional bearer token for an authenticated instance). This module holds
 * that selection in tab-scoped session storage, so the (non-React) recon
 * client can read it synchronously on every call without sharing bearer
 * credentials with other tabs.
 *
 * `url === ''` means "use the server default" (no override header sent).
 */
const KEY = 'recon.endpoint';

export interface ReconEndpoint {
  /** Base URL of the recon server, or '' to use the server-side default. */
  url: string;
  /** Optional bearer token, forwarded as Authorization for authed instances. */
  token?: string;
}

const DEFAULT: ReconEndpoint = { url: '' };

interface EndpointStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

function browserSessionStorage(): EndpointStorage | null {
  if (typeof window === 'undefined') return null;
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

function scrubLegacyPersistentCredential(): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem(KEY);
  } catch {
    /* ignore unavailable persistent storage */
  }
}

function normalizeEndpoint(value: unknown): ReconEndpoint {
  if (!value || typeof value !== 'object') return DEFAULT;
  const candidate = value as { url?: unknown; token?: unknown };
  if (typeof candidate.url !== 'string') return DEFAULT;
  if (candidate.token !== undefined && typeof candidate.token !== 'string') return DEFAULT;
  return {
    url: candidate.url.trim().replace(/\/+$/, ''),
    token: candidate.token?.trim() || undefined,
  };
}

export function getReconEndpoint(storage?: EndpointStorage | null): ReconEndpoint {
  const selectedStorage = storage === undefined ? browserSessionStorage() : storage;
  if (!selectedStorage) return DEFAULT;
  if (storage === undefined) scrubLegacyPersistentCredential();
  try {
    const raw = selectedStorage.getItem(KEY);
    return raw ? normalizeEndpoint(JSON.parse(raw)) : DEFAULT;
  } catch {
    return DEFAULT;
  }
}

export function setReconEndpoint(
  endpoint: ReconEndpoint,
  storage?: EndpointStorage | null,
): void {
  const selectedStorage = storage === undefined ? browserSessionStorage() : storage;
  if (!selectedStorage) return;
  if (storage === undefined) scrubLegacyPersistentCredential();
  const normalized = normalizeEndpoint(endpoint);
  try {
    if (!normalized.url && !normalized.token) selectedStorage.removeItem(KEY);
    else selectedStorage.setItem(KEY, JSON.stringify(normalized));
  } catch {
    /* ignore tab-storage failures (private mode etc.) */
  }
}

/** Short host label for display (e.g. "localhost:8080" or the server default). */
export function reconEndpointLabel(ep: ReconEndpoint = getReconEndpoint()): string {
  if (!ep.url) return 'Local (default)';
  try {
    return new URL(ep.url).host;
  } catch {
    return ep.url;
  }
}
