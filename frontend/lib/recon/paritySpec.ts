/**
 * Pure (de)serialization for a `source_parity` templateSpec side.
 *
 * A ledger side reads posting-derived balances. An account-metadata side reads
 * one opaque metadata key and labels that integer value with one independently
 * declared ledger asset. No relationship is inferred between the key and asset.
 */

export type ParitySourceKind = 'ledger' | 'account_metadata';

export const PARITY_SOURCE_KINDS: { value: ParitySourceKind; label: string }[] = [
  { value: 'ledger', label: 'Posting-derived balance' },
  { value: 'account_metadata', label: 'Account metadata balance' },
];

/** Uppercase base (1–17 chars), optional precision from 1 through 255. */
export const ASSET_CODE_RE = /^[A-Z][A-Z0-9]{0,16}(\/([1-9][0-9]{0,2}))?$/;

export function isValidAssetCode(code: string): boolean {
  const match = ASSET_CODE_RE.exec(code.trim());
  if (!match) return false;
  return match[2] === undefined || Number(match[2]) <= 255;
}

export interface ParitySideModel {
  kind: ParitySourceKind;
  ledger: string;
  query: Record<string, unknown>;
  /** Required when kind = account_metadata. */
  metadataKey?: string;
  /** Required when kind = account_metadata. Independent from metadataKey. */
  asset?: string;
}

export function buildParitySide(m: ParitySideModel): Record<string, unknown> {
  const base = { kind: m.kind, ledger: m.ledger, query: m.query };
  if (m.kind !== 'account_metadata') return base;
  return {
    ...base,
    metadataKey: (m.metadataKey ?? '').trim(),
    asset: (m.asset ?? '').trim(),
  };
}

export function parityScope(
  kinds: ParitySourceKind[],
  requested: 'aggregate' | 'per_account',
): 'aggregate' | 'per_account' {
  return kinds.includes('account_metadata') ? 'aggregate' : requested;
}

/** Legacy prefix/allowlist fields are intentionally ignored: no safe mapping exists. */
export function parseParitySide(raw: unknown): {
  kind: ParitySourceKind;
  metadataKey: string;
  asset: string;
} {
  const r = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>;
  return {
    kind: r.kind === 'account_metadata' ? 'account_metadata' : 'ledger',
    metadataKey: typeof r.metadataKey === 'string' ? r.metadataKey : '',
    asset: typeof r.asset === 'string' ? r.asset : '',
  };
}

export function isParitySideComplete(m: { kind: ParitySourceKind; metadataKey?: string; asset?: string }): boolean {
  if (m.kind !== 'account_metadata') return true;
  return !!m.metadataKey?.trim() && !!m.asset?.trim();
}

export function validateParitySide(m: { kind: ParitySourceKind; metadataKey?: string; asset?: string }): string[] {
  if (m.kind !== 'account_metadata') return [];
  const errors: string[] = [];
  if (!m.metadataKey?.trim()) {
    errors.push('Metadata key is required for account metadata sources.');
  }
  const asset = m.asset?.trim() ?? '';
  if (!asset) {
    errors.push('Asset is required for account metadata sources.');
  } else if (!isValidAssetCode(asset)) {
    errors.push(`Asset "${asset}" is not a valid asset code. Use uppercase letters and numbers, with an optional /precision from 1 to 255 such as USD or EURC/6.`);
  }
  return errors;
}

export function validateParityPair(
  left: { kind: ParitySourceKind; asset?: string },
  right: { kind: ParitySourceKind; asset?: string },
): string[] {
  if (left.kind !== 'account_metadata' || right.kind !== 'account_metadata') return [];
  const leftAsset = left.asset?.trim() ?? '';
  const rightAsset = right.asset?.trim() ?? '';
  if (!leftAsset || !rightAsset || leftAsset === rightAsset) return [];
  return [`Account metadata sources must declare the same asset (Source A: ${leftAsset}; Source B: ${rightAsset}).`];
}
