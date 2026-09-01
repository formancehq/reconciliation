'use client';

/**
 * Fetch a data ledger's *indexed* account-metadata keys (+ value kind) so the
 * rule builder can offer a real key picker with type-appropriate operators.
 *
 * Sourced through the reconciliation module (GET /ledgers/{ledger}/meta-fields),
 * which reads the ledger over its own gRPC connection — the standalone UI has no
 * browser→ledger connection. Best-effort + per-ledger cached: an unknown ledger
 * (or a backend that can't reach it) yields [] and the builder falls back to a
 * free-text key.
 */
import { useEffect, useState } from 'react';
import { reconRequest } from '@/lib/recon/client';
import type { AccountTypeView, MetaValueKind } from '@/lib/ledger-filter';

export interface MetaFieldOption {
  key: string;
  kind: MetaValueKind;
  /** Index build state; only 'ready' keys are actually queryable. */
  ready: boolean;
}

export interface LedgerFieldOptions {
  fields: MetaFieldOption[];
  accountTypes: AccountTypeView[];
  addressIndexed: boolean;
}

interface MetaFieldsResponse {
  data: LedgerFieldOptions;
}

const EMPTY_OPTIONS: LedgerFieldOptions = { fields: [], accountTypes: [], addressIndexed: false };

export function useLedgerMetaFields(ledger: string): LedgerFieldOptions & { loading: boolean } {
  const name = ledger.trim();
  const [cache, setCache] = useState<Record<string, LedgerFieldOptions>>({});

  useEffect(() => {
    if (!name || cache[name] !== undefined) return;
    let alive = true;
    reconRequest<MetaFieldsResponse>('GET', `/ledgers/${encodeURIComponent(name)}/meta-fields`)
      .then((res) => {
        if (!alive) return;
        const d = res.data ?? EMPTY_OPTIONS;
        setCache((current) => ({
          ...current,
          [name]: {
            fields: d.fields ?? [],
            accountTypes: d.accountTypes ?? [],
            addressIndexed: !!d.addressIndexed,
          },
        }));
      })
      .catch(() => {
        if (alive) setCache((current) => ({ ...current, [name]: EMPTY_OPTIONS }));
      });
    return () => {
      alive = false;
    };
  }, [name, cache]);

  const options = name ? cache[name] : undefined;
  return {
    ...(options ?? EMPTY_OPTIONS),
    loading: !!name && options === undefined,
  };
}
