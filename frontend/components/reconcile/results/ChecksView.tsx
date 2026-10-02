'use client';

/**
 * Validation of a rule's runs, as the `lettering` wrapper does it:
 *
 * - `check` on every run: its shape (sql/shape.sql), then check.sql on a complete run. An
 *   incomplete run shows its reason and has nothing else to check.
 * - `check-chain` on every complete run that names a previous run: the earlier run must be a
 *   link (sql/shape.sql), then check-chain.sql. Another earlier run can be picked, to see what
 *   the chain rules report on a wrong pair.
 */
import { useEffect, useState, type ReactNode } from 'react';
import { Loader2 } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useReconResource } from '@/lib/recon';
import { checkChain, checkRun, runPath, type CheckResult, type RuleRun, type Violation } from '@/lib/lettering/read';
import { formatDay, words } from '@/lib/lettering/format';
import { ErrorState, Loading } from '../ui';
import { DataTable, LetteringVerdictBadge, SectionTitle, Td, Th, THead } from './ui';

type Outcome = CheckResult | Error;

interface Validation {
  check?: Outcome;
  /** Absent while running, null when the run has no chain to check (first run). */
  chain?: Outcome | null;
}

export function ChecksView({
  rule,
  runs,
  selected,
  prev,
  onSelect,
}: {
  rule: string;
  runs: RuleRun[];
  selected?: string;
  prev?: string;
  onSelect: (run: RuleRun, prev?: string) => void;
}) {
  const results = useValidation(rule, runs);
  const done = runs.filter((r) => results[r.run]?.check && results[r.run]?.chain !== undefined).length;
  const run = runs.find((r) => r.run === selected) ?? runs.at(-1);

  return (
    <div className="space-y-6">
      <section>
        <SectionTitle
          title="Runs"
          hint={
            <>
              Every run checked as <code className="font-mono">lettering check</code>, and chained onto the run its
              manifest names as <code className="font-mono">lettering check-chain</code>.
            </>
          }
        >
          <span className="text-xs text-muted-foreground tabular-nums">
            {done < runs.length ? (
              <span className="inline-flex items-center gap-1.5">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> {done} of {runs.length} runs checked
              </span>
            ) : (
              `${runs.length} runs checked`
            )}
          </span>
        </SectionTitle>
        <DataTable minWidth="44rem">
          <THead>
            <Th>Day</Th>
            <Th>Check</Th>
            <Th>Chain</Th>
            <Th>Verdict</Th>
            <Th>Run</Th>
          </THead>
          <tbody className="divide-y">
            {runs.map((r) => (
              <tr
                key={r.run}
                className={`cursor-pointer transition-colors hover:bg-accent ${r.run === run?.run ? 'bg-accent/60' : ''}`}
                onClick={() => onSelect(r)}
              >
                <Td className="whitespace-nowrap">
                  <button
                    type="button"
                    className="hover:underline focus-visible:underline focus-visible:outline-none"
                    onClick={(e) => {
                      e.stopPropagation();
                      onSelect(r);
                    }}
                  >
                    {formatDay(r.day)}
                  </button>
                </Td>
                <Td><OutcomeChip outcome={results[r.run]?.check} /></Td>
                <Td>
                  {results[r.run]?.chain === null ? (
                    <span className="text-muted-foreground">first run</span>
                  ) : (
                    <OutcomeChip outcome={results[r.run]?.chain ?? undefined} />
                  )}
                  {r.previousDay && <div className="mt-0.5 text-[10px] text-muted-foreground">onto {formatDay(r.previousDay)}</div>}
                </Td>
                <Td>
                  <LetteringVerdictBadge verdict={r.verdict} />
                  {r.reason && <div className="mt-0.5 text-[10px] text-muted-foreground">{words(r.reason)}</div>}
                </Td>
                <Td className="font-mono whitespace-nowrap">
                  {r.run}
                  {!r.current && <span className="ml-1.5 font-sans text-[10px] text-muted-foreground">not current</span>}
                </Td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      </section>

      {run && (
        <RunChecks
          key={run.run}
          rule={rule}
          run={run}
          runs={runs}
          check={results[run.run]?.check}
          chain={results[run.run]?.chain}
          prev={prev}
          onPrev={(p) => onSelect(run, p)}
        />
      )}
    </div>
  );
}

/** Checks every run, then every chain, one after the other; results arrive as they finish. */
function useValidation(rule: string, runs: RuleRun[]): Record<string, Validation> {
  const [results, setResults] = useState<Record<string, Validation>>({});
  useEffect(() => {
    let cancelled = false;
    const set = (run: string, v: Validation) => {
      if (!cancelled) setResults((all) => ({ ...all, [run]: { ...all[run], ...v } }));
    };
    (async () => {
      for (const r of runs) {
        set(r.run, { check: await checkRun(runPath(rule, r)).catch(toError) });
        if (cancelled) return;
      }
      for (const r of runs) {
        const prev = runs.find((p) => p.run === r.previousRun);
        set(r.run, {
          chain: r.previousRun && prev ? await checkChain(runPath(rule, prev), runPath(rule, r)).catch(toError) : null,
        });
        if (cancelled) return;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [rule, runs]);
  return results;
}

function toError(e: unknown): Error {
  return e instanceof Error ? e : new Error(String(e));
}

function OutcomeChip({ outcome }: { outcome?: Outcome }) {
  if (!outcome) return <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" aria-label="checking" />;
  if (outcome instanceof Error) return <Badge variant="amber" size="sm">Error</Badge>;
  switch (outcome.status) {
    case 'sound':
      return <Badge variant="valid" size="sm">Sound</Badge>;
    case 'violated':
      return (
        <Badge variant="destructive" size="sm">
          {outcome.violations.length} violation{outcome.violations.length === 1 ? '' : 's'}
        </Badge>
      );
    case 'incomplete':
      return <span className="text-muted-foreground">nothing to check</span>;
    case 'refused':
      return <span className="text-muted-foreground">not a link</span>;
  }
}

function RunChecks({
  rule,
  run,
  runs,
  check,
  chain,
  prev,
  onPrev,
}: {
  rule: string;
  run: RuleRun;
  runs: RuleRun[];
  check?: Outcome;
  chain?: Outcome | null;
  prev?: string;
  onPrev: (prev?: string) => void;
}) {
  const earlier = runs.filter((r) => r.day < run.day);
  const picked = earlier.find((r) => r.run === prev);
  const chosen = picked ?? runs.find((r) => r.run === run.previousRun);
  // The chain onto the run previousRun names is in the summary; another pair is checked here.
  const other = useReconResource<CheckResult | null>(
    () => (picked ? checkChain(runPath(rule, picked), runPath(rule, run)) : Promise.resolve(null)),
    [rule, run.run, picked?.run],
  );

  return (
    <section className="grid gap-6 md:grid-cols-2">
      <div className="min-w-0">
        <SectionTitle
          title={`Check · ${formatDay(run.day)}`}
          hint={<span className="font-mono">lettering check …/day={run.day}/run={run.run}</span>}
        />
        <CheckDetail outcome={check} />
      </div>
      <div className="min-w-0">
        <SectionTitle
          title="Chain"
          hint={<span className="font-mono">lettering check-chain {chosen ? `…/run=${chosen.run}` : '<earlier>'} …/run={run.run}</span>}
        >
          {earlier.length > 0 && run.verdict !== 'incomplete' && (
            <Select value={chosen?.run ?? ''} onValueChange={(v) => onPrev(v === run.previousRun ? undefined : v)}>
              <SelectTrigger className="h-8 w-full text-xs sm:w-64" aria-label="Earlier run">
                <SelectValue placeholder="Earlier run" />
              </SelectTrigger>
              <SelectContent>
                {earlier.map((r) => (
                  <SelectItem key={r.run} value={r.run} className="text-xs">
                    {formatDay(r.day)} <span className="font-mono text-muted-foreground">{r.run}</span>
                    {r.run === run.previousRun && <span className="text-muted-foreground"> · previousRun</span>}
                    {r.verdict === 'incomplete' && <span className="text-muted-foreground"> · incomplete</span>}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </SectionTitle>
        {picked ? (
          other.loading ? (
            <Loading label="Checking the chain…" />
          ) : other.error ? (
            <ErrorState error={other.error} onRetry={other.refetch} />
          ) : (
            <CheckDetail outcome={other.data ?? undefined} />
          )
        ) : chain === null ? (
          <Notice>First run: it has no previousRun, so there is no chain to check.</Notice>
        ) : (
          <CheckDetail outcome={chain} />
        )}
      </div>
    </section>
  );
}

function CheckDetail({ outcome }: { outcome?: Outcome }) {
  if (!outcome) return <Loading label="Checking…" />;
  if (outcome instanceof Error) return <ErrorState error={outcome} />;
  return (
    <div className="space-y-2">
      <Notice tone={outcome.status === 'violated' ? 'bad' : outcome.status === 'sound' ? 'good' : undefined}>
        <span className="font-mono">{outcome.message}</span>
      </Notice>
      {outcome.violations.length > 0 && <ViolationsTable violations={outcome.violations} />}
    </div>
  );
}

function Notice({ tone, children }: { tone?: 'good' | 'bad'; children: ReactNode }) {
  const color =
    tone === 'good'
      ? 'border-green-foreground/30 bg-green-background/40'
      : tone === 'bad'
        ? 'border-destructive-foreground/30 bg-destructive/10'
        : 'bg-muted/35';
  return <p className={`rounded-md border px-3 py-2 text-xs break-words ${color}`}>{children}</p>;
}

function ViolationsTable({ violations }: { violations: Violation[] }) {
  return (
    <DataTable minWidth="36rem">
      <THead>
        <Th>Rule</Th>
        <Th>Key</Th>
        <Th>Detail</Th>
      </THead>
      <tbody className="divide-y">
        {violations.map((v, i) => (
          <tr key={i}>
            <Td className="font-mono whitespace-nowrap">{v.rule}</Td>
            <Td className="font-mono whitespace-nowrap">{v.key}</Td>
            <Td className="min-w-72 break-words">{v.detail}</Td>
          </tr>
        ))}
      </tbody>
    </DataTable>
  );
}
