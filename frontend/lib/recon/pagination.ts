import type { Cursor } from './types';

export class ReconPaginationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ReconPaginationError';
  }
}

/**
 * Traverse a Reconciliation cursor collection without returning incomplete
 * audit data when the server emits a malformed or looping continuation.
 */
export async function collectReconPages<T>(
  fetchPage: (cursor?: string) => Promise<Cursor<T>>,
  maxPages = 100,
): Promise<T[]> {
  const items: T[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;

  for (let pageNumber = 0; pageNumber < maxPages; pageNumber++) {
    const page = await fetchPage(cursor);
    items.push(...(page.data ?? []));
    if (!page.hasMore) return items;
    if (!page.next) {
      throw new ReconPaginationError(
        'Reconciliation page says more data exists but has no next cursor',
      );
    }
    if (seenCursors.has(page.next)) {
      throw new ReconPaginationError(
        'Reconciliation pagination returned a repeated cursor',
      );
    }
    seenCursors.add(page.next);
    cursor = page.next;
  }

  throw new ReconPaginationError(
    `Reconciliation pagination exceeded the ${maxPages} page safety limit`,
  );
}
