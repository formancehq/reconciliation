'use client';

/**
 * Typed metadata filter builder for a recon query.
 *
 * Picks from the target ledger's *indexed* account metadata keys (falling back
 * to a free-text key + manual type when the ledger isn't connected), offers the
 * operators the key's type allows, and compiles to the recon query DSL:
 *   {$match|$gt|$gte|$lt|$lte|$exists : { "metadata[key]": value }}
 * `between` compiles to {$and:[{$gte},{$lte}]}. Operator/type mapping is reused
 * from the shared ledger-client helpers so it matches the explorer's builder.
 * (The recon resolver validates key/index/type at rule create — see the 400.)
 */
import { useCallback } from 'react';
import { Plus, Trash2, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
  metadataOperatorsForKind, V3_OPERATOR_LABELS,
  type FilterOperator, type MetaValueKind, type V3GrpcLedgerClient,
} from '@/lib/ledger-filter';
import { ValueCombobox } from '@/components/explorer-v3/ValueCombobox';
import { useV3Adapter } from '@/lib/connection/provider';
import { useLedgerMetaFields } from './useLedgerMetaFields';
import {
  emptyMetaRule,
  type MetaRule, type MetaCombinator,
} from '@/lib/recon/querySpec';
export { compileReconQuery, emptyMetaRule, parseReconQuery } from '@/lib/recon/querySpec';
export type { MetaRule, MetaCombinator } from '@/lib/recon/querySpec';

const CUSTOM = '__custom__';
const KIND_LABEL: Record<MetaValueKind, string> = { string: 'text', int: 'integer', uint: 'integer', bool: 'boolean', datetime: 'datetime' };
const ALL_KINDS: MetaValueKind[] = ['string', 'int', 'bool', 'datetime'];

// ── Component ────────────────────────────────────────────────────────────────
export function MetaFilterBuilder({ ledger, rules, setRules, combinator, setCombinator }: {
  ledger: string;
  rules: MetaRule[];
  setRules: (r: MetaRule[]) => void;
  combinator: MetaCombinator;
  setCombinator: (c: MetaCombinator) => void;
}) {
  const adapter = useV3Adapter();
  const { fields, loading } = useLedgerMetaFields(ledger);

  const update = (i: number, patch: Partial<MetaRule>) => setRules(rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));

  const onPickKey = (i: number, value: string) => {
    if (value === CUSTOM) { update(i, { custom: true, key: '', kind: 'string', op: '=' }); return; }
    const f = fields.find((x) => x.key === value);
    const kind = f?.kind ?? 'string';
    const ops = metadataOperatorsForKind(kind);
    update(i, { custom: false, key: value, kind, op: ops.includes(rules[i]!.op) ? rules[i]!.op : ops[0]! });
  };

  const onPickKind = (i: number, kind: MetaValueKind) => {
    const ops = metadataOperatorsForKind(kind);
    update(i, { kind, op: ops.includes(rules[i]!.op) ? rules[i]!.op : ops[0]! });
  };

  return (
    <div className="space-y-2">
      {rules.map((r, i) => {
        const ops = metadataOperatorsForKind(r.kind);
        const field = fields.find((option) => option.key === r.key);
        const known = !r.custom && !!field;
        return (
          <div key={i} className="flex flex-wrap items-center gap-1.5">
            {/* key */}
            {known ? (
              <Select value={r.key || undefined} onValueChange={(v) => onPickKey(i, v)}>
                <SelectTrigger className="w-56"><SelectValue placeholder={loading ? 'Loading keys…' : 'metadata key'} /></SelectTrigger>
                <SelectContent>
                  {fields.map((f) => (
                    <SelectItem key={f.key} value={f.key}>
                      {f.key} <span className="text-muted-foreground">· {KIND_LABEL[f.kind]}{f.ready ? '' : ' (building)'}</span>
                    </SelectItem>
                  ))}
                  <SelectItem value={CUSTOM}>Custom key…</SelectItem>
                </SelectContent>
              </Select>
            ) : (
              <>
                <Input className="w-56" value={r.key} onChange={(e) => update(i, { key: e.target.value })} placeholder="metadata key (e.g. ledger.mews.com/account-type)" />
                <Select value={r.kind} onValueChange={(v) => onPickKind(i, v as MetaValueKind)}>
                  <SelectTrigger className="w-24"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {ALL_KINDS.map((k) => <SelectItem key={k} value={k}>{KIND_LABEL[k]}</SelectItem>)}
                  </SelectContent>
                </Select>
              </>
            )}

            {/* operator */}
            <Select value={r.op} onValueChange={(v) => update(i, { op: v as FilterOperator })}>
              <SelectTrigger className="w-24"><SelectValue /></SelectTrigger>
              <SelectContent>
                {ops.map((op) => <SelectItem key={op} value={op}>{V3_OPERATOR_LABELS[op]}</SelectItem>)}
              </SelectContent>
            </Select>

            {/* value(s) */}
            <ValueInput rule={r} which="value" ledger={ledger} indexedReady={field?.ready === true} adapter={adapter} onChange={(v) => update(i, { value: v })} />
            {r.op === 'between' && (
              <>
                <span className="text-xs text-muted-foreground">and</span>
                <ValueInput rule={r} which="value2" ledger={ledger} indexedReady={field?.ready === true} adapter={adapter} onChange={(v) => update(i, { value2: v })} />
              </>
            )}

            <Button type="button" size="icon-sm" variant="ghost" onClick={() => setRules(rules.filter((_, idx) => idx !== i))} aria-label="Remove condition">
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        );
      })}

      <div className="flex items-center gap-2">
        <Button type="button" size="sm" variant="ghost" className="h-7 text-xs text-muted-foreground" onClick={() => setRules([...rules, emptyMetaRule()])}>
          <Plus className="mr-1 h-3.5 w-3.5" /> Add metadata condition
        </Button>
        {loading && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}
        {rules.length > 1 && (
          <Select value={combinator} onValueChange={(v) => setCombinator(v as MetaCombinator)}>
            <SelectTrigger className="h-7 w-28 text-xs"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="and">match all</SelectItem>
              <SelectItem value="or">match any</SelectItem>
            </SelectContent>
          </Select>
        )}
      </div>
    </div>
  );
}

function ValueInput({ rule, which, ledger, indexedReady, adapter, onChange }: {
  rule: MetaRule;
  which: 'value' | 'value2';
  ledger: string;
  indexedReady: boolean;
  adapter: V3GrpcLedgerClient | null;
  onChange: (v: string) => void;
}) {
  const v = which === 'value' ? rule.value : (rule.value2 ?? '');
  const fetchValues = useCallback((search: string) => {
    if (!adapter || !ledger.trim() || !rule.key.trim()) return Promise.resolve({ values: [], capped: false });
    return adapter.distinctMetadataValues({ ledger: ledger.trim(), target: 'accounts', key: rule.key.trim(), search, checkpointId: 0n });
  }, [adapter, ledger, rule.key]);

  if (rule.op === 'exists') return null;
  if (rule.kind === 'bool') {
    return (
      <Select value={rule.value || 'true'} onValueChange={onChange}>
        <SelectTrigger className="w-24"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="true">true</SelectItem>
          <SelectItem value="false">false</SelectItem>
        </SelectContent>
      </Select>
    );
  }
  if (rule.kind === 'datetime') {
    return <Input type="datetime-local" className="w-52" value={v} onChange={(e) => onChange(e.target.value)} />;
  }
  const numeric = rule.kind === 'int' || rule.kind === 'uint';
  if (which === 'value' && rule.kind === 'string' && rule.op === '=' && indexedReady && adapter) {
    return <ValueCombobox value={v} onChange={onChange} placeholder="value" className="w-40 font-mono" modal fetchValues={fetchValues} />;
  }
  return (
    <Input
      className="w-40"
      inputMode={numeric ? 'numeric' : 'text'}
      value={v}
      onChange={(e) => onChange(e.target.value)}
      placeholder={numeric ? 'value' : 'value'}
    />
  );
}
