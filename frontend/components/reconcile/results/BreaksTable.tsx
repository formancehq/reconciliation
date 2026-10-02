'use client';

/**
 * A run's breaks file (results doc §6), in file order (open before resolved, then
 * priority, then |amount|), with the filters recon's API will page it by: class,
 * priority and lifecycle.
 */
import { useMemo, useState } from 'react';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { FILTER_TOOLBAR } from '@/lib/uiClasses';
import type { Cell, Rows } from '@/lib/lettering/duckdb';
import { formatDay, words } from '@/lib/lettering/format';
import { Amount, DataTable, LifecycleBadge, PriorityBadge, SectionTitle, Td, Th, THead } from './ui';

const ALL = '*';

type Break = Record<string, Cell>;

/** What the break is about: the payment reference, the hold, or the account and direction. */
function subject(b: Break): { main: string; sub?: string } {
  if (b.leg === 'flow') {
    const holds = Array.isArray(b.product)
      ? [...new Set(b.product.map((p) => (p as Record<string, Cell>).holdId as string))].sort()
      : [];
    return { main: String(b.ref), sub: holds.length > 0 ? `→ ${holds.join(', ')}` : b.pairedHold ? `names ${b.pairedHold}` : undefined };
  }
  if (b.leg === 'stock') return { main: String(b.holdId), sub: `${b.side} · ${b.ageDays} days old` };
  return { main: String(b.account), sub: `${b.direction}` };
}

export function BreaksTable({ breaks }: { breaks: Rows }) {
  const [cls, setCls] = useState(ALL);
  const [priority, setPriority] = useState(ALL);
  const [lifecycle, setLifecycle] = useState(ALL);

  const options = useMemo(() => {
    const distinct = (key: string) => [...new Set(breaks.rows.map((r) => String(r[key])))].sort();
    return { class: distinct('class'), priority: distinct('priority'), lifecycle: distinct('lifecycle') };
  }, [breaks]);

  const rows = breaks.rows.filter(
    (r) =>
      (cls === ALL || r.class === cls) &&
      (priority === ALL || String(r.priority) === priority) &&
      (lifecycle === ALL || r.lifecycle === lifecycle),
  );
  const open = breaks.rows.filter((r) => r.outcome === 'break').length;

  return (
    <section>
      <SectionTitle
        title="Breaks"
        hint={`${open} open, ${breaks.rows.length - open} resolved since the previous run · breaks.ndjson.gz`}
      />
      <div className={`${FILTER_TOOLBAR} mb-2`}>
        <Filter label="Class" value={cls} onChange={setCls} values={options.class} render={words} />
        <Filter label="Priority" value={priority} onChange={setPriority} values={options.priority} render={(p) => `P${p}`} />
        <Filter label="Lifecycle" value={lifecycle} onChange={setLifecycle} values={options.lifecycle} render={words} />
        <span className="ml-auto text-xs text-muted-foreground tabular-nums">
          {rows.length} of {breaks.rows.length}
        </span>
      </div>
      {breaks.rows.length === 0 ? (
        <p className="rounded-md border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">
          No break in this run.
        </p>
      ) : (
        <DataTable minWidth="44rem">
          <THead>
            <Th>Priority</Th>
            <Th>Class</Th>
            <Th right>Amount</Th>
            <Th>Item</Th>
            <Th>Lifecycle</Th>
            <Th>Opened</Th>
            <Th>Leg</Th>
          </THead>
          <tbody className="divide-y">
            {rows.map((b) => {
              const s = subject(b);
              return (
                <tr key={String(b.breakId)} className={b.outcome === 'break' ? undefined : 'text-muted-foreground'}>
                  <Td><PriorityBadge priority={Number(b.priority)} /></Td>
                  <Td className="whitespace-nowrap">{words(String(b.class))}</Td>
                  <Td right className="whitespace-nowrap">
                    <Amount value={b.amount as bigint} asset={String(b.asset)} />{' '}
                    <span className="text-[10px] text-muted-foreground">{String(b.asset)}</span>
                  </Td>
                  <Td className="min-w-40">
                    <div className="font-mono break-all">{s.main}</div>
                    {s.sub && <div className="text-[10px] break-all text-muted-foreground">{s.sub}</div>}
                  </Td>
                  <Td><LifecycleBadge lifecycle={String(b.lifecycle)} /></Td>
                  <Td className="whitespace-nowrap">
                    {formatDay(b.openedOn as string)}
                    {b.resolvedOn && <div className="text-[10px] text-muted-foreground">resolved {formatDay(b.resolvedOn as string)}</div>}
                  </Td>
                  <Td className="text-muted-foreground">{String(b.leg)}</Td>
                </tr>
              );
            })}
            {rows.length === 0 && (
              <tr>
                <td colSpan={7} className="py-6 text-center text-xs text-muted-foreground">No break matches the filters.</td>
              </tr>
            )}
          </tbody>
        </DataTable>
      )}
    </section>
  );
}

function Filter({
  label,
  value,
  onChange,
  values,
  render,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  values: string[];
  render: (v: string) => string;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="h-8 w-full text-xs sm:w-44" aria-label={label}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ALL} className="text-xs">
          {label}: all
        </SelectItem>
        {values.map((v) => (
          <SelectItem key={v} value={v} className="text-xs">
            {render(v)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
