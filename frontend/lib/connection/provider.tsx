'use client';

/**
 * Ledger-connection seam.
 *
 * The console UI drew its rule-builder autosuggest from a LIVE browser→ledger
 * gRPC connection. The standalone reconciliation app has no such connection —
 * it is a plain HTTP client of the reconciliation module, which owns the ledger
 * gRPC connection. So the connected-only VALUE conveniences (per-key value
 * autosuggest) degrade to manual entry, while the introspection the module can
 * serve over HTTP — indexed metadata KEYS and the ledger-name list — is sourced
 * through the module instead (see the backend's /ledgers* endpoints and
 * useLedgerMetaFields).
 */

import type { V3GrpcLedgerClient } from '@/lib/ledger-filter';
import { reconRequest } from '@/lib/recon/client';

/** Best-effort ledger-name suggestion client. */
export interface LedgerNameClient {
  listLedgers(): Promise<{ name: string }[]>;
}

interface LedgersResponse {
  data?: { ledgers?: { name: string }[] };
}

/**
 * Ledger-name suggestion client backed by the reconciliation module: listLedgers
 * fetches GET /ledgers, which reads the live ledger names over the module's own
 * gRPC connection. A module-level singleton so `useLedgerClient()` returns a
 * stable identity across renders — the create dialogs key their fetch effect and
 * a client-identity guard on it. Best-effort: a failed fetch / empty list leaves
 * the dialogs' ledger field free-text.
 */
const reconLedgerClient: LedgerNameClient = {
  async listLedgers() {
    const res = await reconRequest<LedgersResponse>('GET', '/ledgers');
    return res.data?.ledgers ?? [];
  },
};

/** Value-autosuggest adapter — null here (no browser→ledger connection). */
export function useV3Adapter(): V3GrpcLedgerClient | null {
  return null;
}

/** Raw v3 client — null here (metadata keys come from the module over HTTP). */
export function useV3Client(): null {
  return null;
}

/**
 * Ledger-name suggestion client — the recon-backed singleton, so both create
 * dialogs populate their ledger dropdown from GET /ledgers (falling back to
 * free-text if it yields nothing).
 */
export function useLedgerClient(): LedgerNameClient | null {
  return reconLedgerClient;
}
