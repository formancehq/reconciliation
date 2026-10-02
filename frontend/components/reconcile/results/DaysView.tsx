'use client';

/**
 * A rule over its days: each day's current run and its verdict, from the tool's
 * `current-runs` query (rule.sql `current_runs`: the latest complete run). The
 * runs a day's current run replaced, or that concluded nothing, are listed with
 * it.
 */
import { ChevronRight } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import type { RuleRun } from '@/lib/lettering/read';
import { formatDay, LETTERING_VERDICT_META, words } from '@/lib/lettering/format';
import type { LetteringVerdict } from '@/lib/lettering/manifest';
import { DataTable, LetteringVerdictBadge, SectionTitle, Td, Th, THead } from './ui';

export function DaysView({ runs, onOpen }: { runs: RuleRun[]; onOpen: (day: string, run?: string) => void }) {
  const days = [...new Set(runs.map((r) => r.day))];
  const current = runs.filter((r) => r.current);
  const tally = Object.entries(
    current.reduce<Record<string, number>>((acc, r) => ({ ...acc, [r.verdict]: (acc[r.verdict] ?? 0) + 1 }), {}),
  );

  return (
    <section>
      <SectionTitle
        title="Days"
        hint={`${days.length} days, ${runs.length} runs. A day's current run is its latest complete run; it replaces the runs before it.`}
      >
        <div className="flex flex-wrap gap-1.5">
          {tally.map(([verdict, n]) => (
            <Badge key={verdict} variant={LETTERING_VERDICT_META[verdict as LetteringVerdict]?.variant ?? 'outline'} size="sm">
              {n} {LETTERING_VERDICT_META[verdict as LetteringVerdict]?.label ?? words(verdict)}
            </Badge>
          ))}
        </div>
      </SectionTitle>
      <DataTable minWidth="36rem">
        <THead>
          <Th>Day</Th>
          <Th>Verdict</Th>
          <Th>Current run</Th>
          <Th>Other runs</Th>
          <Th />
        </THead>
        <tbody className="divide-y">
          {days.map((day) => {
            const dayRuns = runs.filter((r) => r.day === day);
            const cur = dayRuns.find((r) => r.current);
            const others = dayRuns.filter((r) => r !== cur);
            return (
              <tr
                key={day}
                className="cursor-pointer transition-colors hover:bg-accent"
                onClick={() => onOpen(day, cur?.run)}
              >
                <Td className="whitespace-nowrap font-medium">
                  <button
                    type="button"
                    className="hover:underline focus-visible:underline focus-visible:outline-none"
                    onClick={(e) => {
                      e.stopPropagation();
                      onOpen(day, cur?.run);
                    }}
                  >
                    {formatDay(day)}
                  </button>
                </Td>
                <Td>
                  {cur ? (
                    <LetteringVerdictBadge verdict={cur.verdict} />
                  ) : (
                    <span className="text-muted-foreground">no complete run</span>
                  )}
                </Td>
                <Td className="font-mono whitespace-nowrap">{cur?.run ?? '—'}</Td>
                <Td>
                  {others.length === 0 ? (
                    <span className="text-muted-foreground">—</span>
                  ) : (
                    <ul className="space-y-0.5">
                      {others.map((r) => (
                        <li key={r.run} className="whitespace-nowrap">
                          <span className="font-mono">{r.run}</span>{' '}
                          <span className="text-muted-foreground">
                            {r.verdict === 'incomplete'
                              ? `incomplete${r.reason ? `: ${words(r.reason)}` : ''}`
                              : `${words(r.verdict)}, replaced`}
                          </span>
                        </li>
                      ))}
                    </ul>
                  )}
                </Td>
                <Td className="w-6 text-muted-foreground">
                  <ChevronRight className="h-3.5 w-3.5" aria-hidden />
                </Td>
              </tr>
            );
          })}
        </tbody>
      </DataTable>
    </section>
  );
}
