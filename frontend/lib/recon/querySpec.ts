import type { FilterOperator, MetaValueKind } from '@/lib/ledger-filter';

/** Framework-free representation of one account metadata selector. */
export interface MetaRule {
  key: string;
  kind: MetaValueKind;
  op: FilterOperator;
  value: string;
  value2?: string;
  custom?: boolean;
}

export type MetaCombinator = 'and' | 'or';

export const emptyMetaRule = (): MetaRule => ({ key: '', kind: 'string', op: '=', value: '', custom: false });

const OP_JSON: Partial<Record<FilterOperator, string>> = { '>': '$gt', '>=': '$gte', '<': '$lt', '<=': '$lte' };
const JSON_TO_OP: Record<string, FilterOperator> = { $gt: '>', $gte: '>=', $lt: '<', $lte: '<=' };
const micros = (s?: string) => { const t = Date.parse(s ?? ''); return Number.isNaN(t) ? NaN : t * 1000; };
const scalarNum = (r: MetaRule, s?: string) => (r.kind === 'datetime' ? micros(s) : Number(s));
const kindOfValue = (v: unknown): MetaValueKind =>
  typeof v === 'boolean' ? 'bool' : typeof v === 'number' ? 'int' : 'string';
const one = (o: unknown): [string, unknown] | null => {
  if (!o || typeof o !== 'object') return null;
  const entries = Object.entries(o as Record<string, unknown>);
  return entries.length === 1 ? entries[0]! : null;
};
const stripMetaKey = (field: string): string | null =>
  field.startsWith('metadata[') && field.endsWith(']') ? field.slice(9, -1) : null;

function compileMetaRule(r: MetaRule): Record<string, unknown> | null {
  const key = r.key.trim();
  if (!key) return null;
  const field = `metadata[${key}]`;
  if (r.op === 'exists') return { $exists: { [field]: true } };
  if (r.op === '~') return null;
  if (r.op === 'between') {
    const lo = scalarNum(r, r.value); const hi = scalarNum(r, r.value2);
    if (Number.isNaN(lo) || Number.isNaN(hi)) return null;
    return { $and: [{ $gte: { [field]: lo } }, { $lte: { [field]: hi } }] };
  }
  if (r.op === '=') {
    if (r.kind === 'bool') return { $match: { [field]: r.value === 'true' } };
    if (r.kind === 'int' || r.kind === 'uint' || r.kind === 'datetime') {
      const value = scalarNum(r, r.value);
      return Number.isNaN(value) ? null : { $match: { [field]: value } };
    }
    return r.value === '' ? null : { $match: { [field]: r.value } };
  }
  const opJson = OP_JSON[r.op];
  if (!opJson) return null;
  const value = scalarNum(r, r.value);
  return Number.isNaN(value) ? null : { [opJson]: { [field]: value } };
}

/** Address selector + metadata rules to the Reconcile query JSON contract. */
export function compileReconQuery(address: string, rules: MetaRule[], combinator: MetaCombinator): Record<string, unknown> {
  const addr = { $match: { address: address.trim() || '*' } };
  const metas = rules.map(compileMetaRule).filter((x): x is Record<string, unknown> => x !== null);
  if (metas.length === 0) return addr;
  if (combinator === 'or') return { $and: [addr, { $or: metas }] };
  return { $and: [addr, ...metas] };
}

function nodeToMetaRule(node: unknown): MetaRule | null {
  const top = one(node);
  if (!top) return null;
  const [op, inner] = top;
  if (op === '$and' && Array.isArray(inner) && inner.length === 2) {
    const gte = inner.map(one).find((x) => x?.[0] === '$gte')?.[1];
    const lte = inner.map(one).find((x) => x?.[0] === '$lte')?.[1];
    const lo = gte ? one(gte) : null;
    const hi = lte ? one(lte) : null;
    if (lo && hi) {
      const key = stripMetaKey(lo[0]);
      if (key) return { key, kind: kindOfValue(lo[1]), op: 'between', value: String(lo[1]), value2: String(hi[1]) };
    }
    return null;
  }
  const leaf = one(inner);
  if (!leaf) return null;
  const key = stripMetaKey(leaf[0]);
  if (!key) return null;
  const value = leaf[1];
  if (op === '$match') return { key, kind: kindOfValue(value), op: '=', value: String(value) };
  if (op === '$exists') return { key, kind: 'bool', op: 'exists', value: '' };
  if (JSON_TO_OP[op]) return { key, kind: kindOfValue(value), op: JSON_TO_OP[op]!, value: String(value) };
  return null;
}

/** Best-effort inverse for the query shapes emitted by compileReconQuery. */
export function parseReconQuery(raw: unknown): { address: string; meta: MetaRule[]; metaComb: MetaCombinator } {
  let query: unknown = raw;
  if (typeof query === 'string') { try { query = JSON.parse(query); } catch { query = {}; } }
  const empty = { address: '', meta: [] as MetaRule[], metaComb: 'and' as MetaCombinator };
  if (!query || typeof query !== 'object') return empty;
  const obj = query as Record<string, unknown>;
  const addrOf = (node: unknown): string | null => {
    const match = one(node);
    if (match?.[0] === '$match') {
      const inner = one(match[1]);
      if (inner?.[0] === 'address') return String(inner[1]);
    }
    return null;
  };
  const bare = addrOf(obj);
  if (bare !== null) return { address: bare, meta: [], metaComb: 'and' };
  if (Array.isArray(obj.$and)) {
    let address = '';
    let metaComb: MetaCombinator = 'and';
    const meta: MetaRule[] = [];
    for (const node of obj.$and) {
      const selectedAddress = addrOf(node);
      if (selectedAddress !== null) { address = selectedAddress; continue; }
      const orNode = one(node);
      if (orNode?.[0] === '$or' && Array.isArray(orNode[1])) {
        metaComb = 'or';
        for (const candidate of orNode[1]) { const parsed = nodeToMetaRule(candidate); if (parsed) meta.push(parsed); }
        continue;
      }
      const parsed = nodeToMetaRule(node);
      if (parsed) meta.push(parsed);
    }
    return { address, meta, metaComb };
  }
  const single = nodeToMetaRule(obj);
  return single ? { address: '', meta: [single], metaComb: 'and' } : empty;
}
