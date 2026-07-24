/**
 * Pure filter-builder vocabulary shared by the rule builder UI.
 *
 * These are the connection-independent bits extracted from the console's
 * `@workspace/ledger-client` v3 filter builder: operator tokens, metadata value
 * kinds, and the operators offered per kind. The heavier compilation/streaming
 * helpers (which needed a live browser→ledger gRPC client) are not used here —
 * the standalone app sources indexed metadata keys through the reconciliation
 * backend instead (GET /ledgers/{ledger}/meta-fields).
 */

/** Operator tokens shared by the builder UI and the recon query DSL. */
export type FilterOperator = '=' | '<' | '<=' | '>' | '>=' | 'between' | '~' | 'exists';

/** The value kind of a metadata key, derived from its declared schema type. */
export type MetaValueKind = 'string' | 'int' | 'uint' | 'bool' | 'datetime';

/** A ledger account type (name + address pattern) surfaced for the picker. */
export interface AccountTypeView {
  name: string;
  pattern: string;
}

/**
 * Minimal shape of the value-autosuggest adapter. In the console this was the
 * live gRPC client; the standalone app has no browser→ledger connection, so
 * `useV3Adapter()` returns null and the builder falls back to manual entry. The
 * type is retained so the builder compiles unchanged.
 */
export interface V3GrpcLedgerClient {
  distinctMetadataValues(opts: {
    ledger: string;
    target: string;
    key: string;
    search: string;
    checkpointId: bigint;
  }): Promise<{ values: string[]; capped: boolean }>;
}

export const V3_OPERATOR_LABELS: Record<FilterOperator, string> = {
  '=': '=',
  '<': '<',
  '<=': '≤',
  '>': '>',
  '>=': '≥',
  between: 'between',
  '~': 'starts with',
  exists: 'exists',
};

/** Operators offered for a metadata key of the given declared/selected kind. */
export const metadataOperatorsForKind = (kind: MetaValueKind): FilterOperator[] =>
  kind === 'int' || kind === 'uint' || kind === 'datetime'
    ? ['=', '<', '<=', '>', '>=', 'between', 'exists']
    : ['=', 'exists'];
