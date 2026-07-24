/**
 * Lightweight data hooks for the recon API (no React Query dependency).
 *
 * `useReconResource` is a minimal fetch-on-mount resource with manual refetch;
 * `poll` handles the read-after-write eventual consistency called out in
 * RECON-API.md § Gotchas — after `POST /evaluate` (or an alert action), the
 * follow-up `GET /alerts` / `GET /captures` may briefly not reflect the write,
 * so we retry a few times over ~1s rather than assume instant.
 */
import { useCallback, useEffect, useRef, useState } from 'react';

export interface ReconResource<T> {
  data: T | undefined;
  error: unknown;
  /** True during the very first load (no data yet). */
  loading: boolean;
  /** True while a refetch is in flight (data may already be shown). */
  refreshing: boolean;
  refetch: () => Promise<void>;
  /** Optimistic/local override of the current data. */
  setData: (updater: T | ((prev: T | undefined) => T)) => void;
}

/**
 * Fetch `fetcher(signal)` on mount and whenever `deps` change. Returns the current
 * value plus a `refetch`. Stale responses (a newer request started meanwhile)
 * are aborted and discarded so out-of-order resolutions can't clobber fresh data.
 */
export function useReconResource<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  deps: unknown[]
): ReconResource<T> {
  const [data, setDataState] = useState<T | undefined>(undefined);
  const [error, setError] = useState<unknown>(undefined);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const reqId = useRef(0);
  const hasData = useRef(false);
  const activeRequest = useRef<AbortController | undefined>(undefined);

  // Keep the latest fetcher without making it a dependency (callers commonly
  // pass an inline closure that changes identity every render). Updated in an
  // effect (not during render) so it's fresh before the deps-effect fires
  // `run` — effects run top-to-bottom, and this is declared first.
  const fetcherRef = useRef(fetcher);
  useEffect(() => { fetcherRef.current = fetcher; });

  const run = useCallback(async () => {
    activeRequest.current?.abort(
      new DOMException("Reconciliation request superseded", "AbortError")
    );
    const controller = new AbortController();
    activeRequest.current = controller;
    const id = ++reqId.current;
    if (hasData.current) setRefreshing(true);
    else setLoading(true);
    try {
      const result = await fetcherRef.current(controller.signal);
      if (id !== reqId.current) return; // superseded
      setDataState(result);
      hasData.current = true;
      setError(undefined);
    } catch (err) {
      if (id !== reqId.current) return;
      if (controller.signal.aborted) return;
      setError(err);
    } finally {
      if (id === reqId.current) {
        activeRequest.current = undefined;
        setLoading(false);
        setRefreshing(false);
      }
    }
  }, []);

  useEffect(() => {
    void run();
    return () => {
      const controller = activeRequest.current;
      if (!controller) return;
      ++reqId.current;
      activeRequest.current = undefined;
      controller.abort(
        new DOMException("Reconciliation request disposed", "AbortError")
      );
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  const setData = useCallback((updater: T | ((prev: T | undefined) => T)) => {
    setDataState((prev) => (typeof updater === 'function' ? (updater as (p: T | undefined) => T)(prev) : updater));
    hasData.current = true;
  }, []);

  return { data, error, loading, refreshing, refetch: run, setData };
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export interface PollOptions<T> {
  /** Total attempts (including the first). Default 3. */
  tries?: number;
  /** Delay between attempts in ms. Default 400 (≈1s across 3 tries). */
  intervalMs?: number;
  /** Stop early when this returns true for a result. Default: never (use last). */
  until?: (value: T) => boolean;
}

/**
 * Call `fn` up to `tries` times, `intervalMs` apart, resolving as soon as
 * `until` accepts a result (or with the final result). Use after a write to
 * absorb read-after-write lag.
 */
export async function poll<T>(fn: () => Promise<T>, opts: PollOptions<T> = {}): Promise<T> {
  const { tries = 3, intervalMs = 400, until } = opts;
  let last = await fn();
  for (let i = 1; i < tries; i++) {
    if (until?.(last)) return last;
    await sleep(intervalMs);
    last = await fn();
  }
  return last;
}
