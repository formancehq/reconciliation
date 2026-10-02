'use client';

/**
 * The queries of tools/lettering-duckdb/queries over every day of a rule, run as
 * `lettering query <name> <rule> [variable=value]` runs them. The list, each
 * question and each variable come from the query files' headers, so a query
 * added to the tool shows up here with no change.
 */
import { useState, type FormEvent, type ReactNode } from 'react';
import { Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { FILTER_TOOLBAR } from '@/lib/uiClasses';
import { useReconResource } from '@/lib/recon';
import type { Cell, Column, Rows } from '@/lib/lettering/duckdb';
import { formatAmount, formatDay } from '@/lib/lettering/format';
import { readQuery, type RuleRun } from '@/lib/lettering/read';
import { QUERIES, type LetteringQuery } from '@/lib/lettering/sql';
import type { LetteringFile } from '@/lib/lettering/source';
import { ErrorState, Loading } from '../ui';
import {
  DataTable,
  LetteringVerdictBadge,
  LifecycleBadge,
  OutcomeBadge,
  PriorityBadge,
  SectionTitle,
  Td,
  Th,
  THead,
} from './ui';

const DEFAULT_QUERY = 'open-breaks';
const LATEST = '*latest';
/** Rows rendered at most; the count says when there are more. */
const MAX_ROWS = 500;

export interface QueryState {
  query?: string;
  day?: string;
  id?: string;
}

export function QueriesView({
  files,
  rule,
  runs,
  state,
  onChange,
}: {
  files: LetteringFile[];
  rule: string;
  runs: RuleRun[];
  state: QueryState;
  onChange: (next: QueryState) => void;
}) {
  const query = QUERIES.find((q) => q.name === state.query) ?? QUERIES.find((q) => q.name === DEFAULT_QUERY) ?? QUERIES[0];
  const days = [...new Set(runs.map((r) => r.day))];
  const complete = new Set(runs.filter((r) => r.current).map((r) => r.day));

  return (
    <div className="space-y-3">
      <div className={FILTER_TOOLBAR}>
        <Select value={query.name} onValueChange={(name) => onChange({ ...state, query: name })}>
          <SelectTrigger className="h-8 w-full text-xs sm:w-56" aria-label="Query">
            <SelectValue>
              <span className="font-mono">{query.name}</span>
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {QUERIES.map((q) => (
              <SelectItem key={q.name} value={q.name} className="text-xs">
                <span className="font-mono">{q.name}</span>
                <span className="ml-1.5 max-w-64 truncate text-muted-foreground">{q.question}</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {query.variables.map((v) =>
          v.name === 'day' ? (
            <Select
              key={v.name}
              value={state.day ?? LATEST}
              onValueChange={(d) => onChange({ ...state, day: d === LATEST ? undefined : d })}
            >
              <SelectTrigger className="h-8 w-full text-xs sm:w-56" aria-label="Day">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={LATEST} className="text-xs">
                  Latest day (default)
                </SelectItem>
                {days.map((d) => (
                  <SelectItem key={d} value={d} className="text-xs">
                    {formatDay(d)}
                    {!complete.has(d) && <span className="ml-1.5 text-muted-foreground">· no complete run</span>}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <IdSearch key={`${v.name}:${state.id ?? ''}`} name={v.name} hint={v.hint} value={state.id} onSubmit={(id) => onChange({ ...state, id })} />
          ),
        )}
      </div>

      <QueryResult
        files={files}
        rule={rule}
        query={query}
        vars={{ day: state.day, id: state.id }}
        latestDay={[...complete].sort().at(-1)}
      />
    </div>
  );
}

function IdSearch({ name, hint, value, onSubmit }: { name: string; hint?: string; value?: string; onSubmit: (v: string) => void }) {
  const [draft, setDraft] = useState(value ?? '');
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (draft.trim()) onSubmit(draft.trim());
  };
  // The hint reads "required: INV-12, PAY-45…": its examples make the placeholder.
  const examples = hint?.replace(/^required:?\s*/, '');
  return (
    <form onSubmit={submit} className="flex w-full gap-2 sm:w-auto">
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        placeholder={examples ? `${name}: ${examples}` : name}
        aria-label={name}
        className="h-8 w-full text-xs sm:w-56"
      />
      <Button type="submit" size="sm" variant="outline" className="h-8" disabled={!draft.trim()}>
        <Search className="h-3.5 w-3.5" />
        <span className="sr-only sm:not-sr-only">Search</span>
      </Button>
    </form>
  );
}

function QueryResult({
  files,
  rule,
  query,
  vars,
  latestDay,
}: {
  files: LetteringFile[];
  rule: string;
  query: LetteringQuery;
  vars: { day?: string; id?: string };
  /** The day a query about one day answers for when `day` is not set (rule.sql `lettering_target_day`). */
  latestDay?: string;
}) {
  const own = Object.fromEntries(query.variables.map((v) => [v.name, vars[v.name as keyof typeof vars]]));
  const missing = query.variables.find((v) => v.required && !own[v.name]);
  const key = JSON.stringify(own);
  const res = useReconResource<Rows | null>(
    () => (missing ? Promise.resolve(null) : readQuery(files, rule, query, own)),
    [files, rule, query.name, key],
  );

  const varsHint = query.variables.length > 0 ? query.variables.map((v) => `${v.name} (${v.hint ?? 'optional'})`).join(', ') : 'none';
  return (
    <section>
      <SectionTitle
        title={query.question}
        hint={
          <>
            {query.detail && <span className="block">{withCode(query.detail)}</span>}
            <span className="block">Variables: {varsHint}</span>
          </>
        }
      />
      {missing ? (
        <p className="rounded-md border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">
          Enter {missing.name} to run <span className="font-mono">{query.name}</span>.
        </p>
      ) : res.loading ? (
        <Loading label={`Running ${query.name}…`} />
      ) : res.error ? (
        <ErrorState error={res.error} onRetry={res.refetch} />
      ) : res.data ? (
        <QueryTable
          rows={res.data}
          empty={
            query.variables.some((v) => v.name === 'day')
              ? `No row on ${formatDay(vars.day ?? latestDay)}${vars.day ? '' : ', the latest day'}.`
              : undefined
          }
        />
      ) : null}
      <details className="mt-2 text-xs">
        <summary className="cursor-pointer text-muted-foreground select-none">
          SQL · tools/lettering-duckdb/queries/{query.name}.sql
        </summary>
        <pre className="mt-2 overflow-x-auto rounded-md border bg-muted/35 p-3 font-mono text-[11px] leading-relaxed">{query.sql}</pre>
      </details>
    </section>
  );
}

/** The headers' `backticks` as code. */
function withCode(text: string): ReactNode[] {
  return text.split('`').map((part, i) =>
    i % 2 === 1 ? (
      <code key={i} className="font-mono text-foreground">
        {part}
      </code>
    ) : (
      part
    ),
  );
}

/** A query's rows, each column rendered by its DuckDB type and its name. */
export function QueryTable({ rows: { columns, rows }, empty = 'No row.' }: { rows: Rows; empty?: string }) {
  if (rows.length === 0) {
    return <p className="rounded-md border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">{empty}</p>;
  }
  const hasAsset = columns.some((c) => c.name === 'asset');
  const shown = rows.slice(0, MAX_ROWS);
  return (
    <>
      <DataTable fit>
        <THead>
          {columns.map((c) => (
            <Th key={c.name} right={isNumeric(c)}>
              {c.name}
              {c.kind === 'amount' && !hasAsset && <span className="ml-1 normal-case">(minor units)</span>}
            </Th>
          ))}
        </THead>
        <tbody className="divide-y">
          {shown.map((row, i) => (
            <tr key={i}>
              {columns.map((c) => (
                <Td key={c.name} right={isNumeric(c)} className="whitespace-nowrap">
                  {renderCell(row[c.name], c, row)}
                </Td>
              ))}
            </tr>
          ))}
        </tbody>
      </DataTable>
      <p className="mt-1 text-[11px] text-muted-foreground tabular-nums">
        {rows.length > MAX_ROWS ? `First ${MAX_ROWS} of ${rows.length} rows` : `${rows.length} row${rows.length === 1 ? '' : 's'}`}
      </p>
    </>
  );
}

function isNumeric(c: Column): boolean {
  return c.kind === 'amount' || c.kind === 'integer' || c.kind === 'number';
}

function renderCell(value: Cell, column: Column, row: Record<string, Cell>): ReactNode {
  if (value === null) return <span className="text-muted-foreground">—</span>;
  switch (column.name) {
    case 'verdict':
      return <LetteringVerdictBadge verdict={String(value)} />;
    case 'priority':
      return <PriorityBadge priority={Number(value)} />;
    case 'lifecycle':
      return <LifecycleBadge lifecycle={String(value)} />;
    case 'outcome':
      return <OutcomeBadge outcome={String(value)} />;
  }
  switch (column.kind) {
    case 'amount':
      return (
        <span className="font-mono tabular-nums">
          {formatAmount(value as bigint, typeof row.asset === 'string' ? row.asset : '')}
        </span>
      );
    case 'integer':
    case 'number':
      return <span className="tabular-nums">{String(value)}</span>;
    case 'boolean':
      return value ? 'yes' : <span className="text-muted-foreground">no</span>;
    case 'nested':
      if (!Array.isArray(value)) return JSON.stringify(value);
      return value.length > 0 ? value.map(String).join(', ') : <span className="text-muted-foreground">—</span>;
    case 'date':
    case 'timestamp':
      return <span className="font-mono">{String(value)}</span>;
    default:
      return String(value);
  }
}
