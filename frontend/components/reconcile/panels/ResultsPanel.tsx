'use client';

/**
 * Results — reads the result files of transaction-level (lettering) rules in the
 * browser with DuckDB-WASM, running the SQL of tools/lettering-duckdb as it is.
 *
 * For one rule: its days and their current runs, one run's statement and breaks,
 * the tool's queries, and the wrapper's checks on every run and chain link.
 *
 * An internal reading and validation prototype: it reads the files of
 * `letteringSource` (lib/lettering/source.ts), today the tool's test data. It
 * does not replace the production path, where the breaks and the statement
 * come from recon's API.
 */
import { BadgeCheck, CalendarDays, FileText, FlaskConical, Files, TerminalSquare } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { FILTER_TOOLBAR, TOOLBAR_SELECT } from '@/lib/uiClasses';
import { useReconResource } from '@/lib/recon';
import { letteringSource, type LetteringFile, type LetteringSource } from '@/lib/lettering/source';
import { readRuleRuns, runPath, type RuleRun } from '@/lib/lettering/read';
import { formatDay } from '@/lib/lettering/format';
import { useReconNav, type ResultsNav, type ResultsSection } from '../ReconContext';
import { EmptyState, ErrorState, Loading } from '../ui';
import { RunView } from '../results/RunView';
import { DaysView } from '../results/DaysView';
import { QueriesView } from '../results/QueriesView';
import { ChecksView } from '../results/ChecksView';

export function ResultsPanel() {
  // The tab is hidden when there is no source; this only keeps the panel honest.
  return letteringSource ? <SourceResults source={letteringSource} /> : null;
}

function SourceResults({ source }: { source: LetteringSource }) {
  const index = useReconResource<string[]>(() => source.rules(), [source]);
  if (index.loading) return <Loading label="Listing the rules…" />;
  if (index.error) return <ErrorState error={index.error} onRetry={index.refetch} />;
  const rules = index.data ?? [];
  if (rules.length === 0) {
    return (
      <div className="p-6">
        <EmptyState icon={<Files className="h-7 w-7" />} title="No result files">
          The source holds no lettering rule.
        </EmptyState>
      </div>
    );
  }
  return <RuleResults source={source} rules={rules} />;
}

// Stable empty values: a new [] on each render would refetch what depends on it.
const NO_FILES: LetteringFile[] = [];
const NO_RUNS: RuleRun[] = [];

interface RuleData {
  /** The rule's files, from the source. */
  files: LetteringFile[];
  runs: RuleRun[];
}

const SECTIONS: { id: ResultsSection; label: string; icon: typeof CalendarDays }[] = [
  { id: 'days', label: 'Days', icon: CalendarDays },
  { id: 'run', label: 'Run', icon: FileText },
  { id: 'queries', label: 'Queries', icon: TerminalSquare },
  { id: 'checks', label: 'Checks', icon: BadgeCheck },
];

function RuleResults({ source, rules }: { source: LetteringSource; rules: string[] }) {
  const { nav, openResults } = useReconNav();
  const state = nav.results ?? {};
  const rule = state.rule && rules.includes(state.rule) ? state.rule : rules[0];
  const section = state.section ?? 'days';
  const go = (next: ResultsNav) => openResults({ ...state, rule, ...next });
  const data = useReconResource<RuleData>(async () => {
    const files = await source.files(rule);
    return { files, runs: await readRuleRuns(files, rule) };
  }, [source, rule]);
  const runs = data.data?.runs ?? NO_RUNS;

  return (
    <div className="mx-auto max-w-6xl min-w-0 space-y-4 p-3 sm:p-4">
      <div className={FILTER_TOOLBAR}>
        {source.testData && (
          <Badge variant="amber" size="sm" title={`Source: ${source.label}`}>
            <FlaskConical className="h-3 w-3" /> Test data
          </Badge>
        )}
        <Picker
          label="Rule"
          value={rule}
          onChange={(r) => openResults({ rule: r, section: state.section, query: state.query })}
          options={rules.map((r) => ({ value: r, label: r }))}
        />
        <Tabs value={section} onValueChange={(v) => go({ section: v as ResultsSection })} className="w-full sm:ml-auto sm:w-auto">
          <TabsList variant="line" className="w-full sm:w-auto">
            {SECTIONS.map((s) => (
              <TabsTrigger key={s.id} value={s.id} className="cursor-pointer text-xs">
                <s.icon className="h-3.5 w-3.5" aria-hidden />
                {s.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </div>

      {data.loading ? (
        <Loading label="Starting DuckDB and reading the rule's manifests…" />
      ) : data.error ? (
        <ErrorState error={data.error} onRetry={data.refetch} />
      ) : runs.length === 0 ? (
        <EmptyState icon={<Files className="h-7 w-7" />} title="No run">
          This rule has no run.
        </EmptyState>
      ) : section === 'days' ? (
        <DaysView runs={runs} onOpen={(day, run) => go({ section: 'run', day, run })} />
      ) : section === 'run' ? (
        <RunSection rule={rule} runs={runs} state={state} go={go} />
      ) : section === 'checks' ? (
        <ChecksView
          rule={rule}
          runs={runs}
          selected={state.run}
          prev={state.prev}
          onSelect={(r, prev) => go({ day: r.day, run: r.run, prev })}
        />
      ) : (
        <QueriesView
          files={data.data?.files ?? NO_FILES}
          rule={rule}
          runs={runs}
          state={{ query: state.query, day: state.day, vars: state.vars }}
          onChange={(q) => go(q)}
        />
      )}
    </div>
  );
}

/** One run: the day's current run unless another run of the day is picked. */
function RunSection({
  rule,
  runs,
  state,
  go,
}: {
  rule: string;
  runs: RuleRun[];
  state: ResultsNav;
  go: (next: ResultsNav) => void;
}) {
  const days = [...new Set(runs.map((r) => r.day))];
  const latestCurrent = [...runs].reverse().find((r) => r.current)?.day;
  const day = state.day && days.includes(state.day) ? state.day : (latestCurrent ?? days.at(-1));
  const dayRuns = runs.filter((r) => r.day === day);
  const run = dayRuns.find((r) => r.run === state.run) ?? dayRuns.find((r) => r.current) ?? dayRuns.at(-1);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <Picker
          label="Day"
          value={day}
          onChange={(d) => go({ day: d, run: undefined })}
          options={days.map((d) => ({ value: d, label: formatDay(d) }))}
        />
        <Picker
          label="Run"
          value={run?.run}
          onChange={(r) => go({ day, run: r })}
          options={dayRuns.map((r) => ({
            value: r.run,
            label: r.run,
            hint: r.current ? 'current' : r.verdict === 'incomplete' ? 'incomplete' : 'replaced',
          }))}
          wide
        />
      </div>
      {run && <RunView key={`${rule}/${run.run}`} path={runPath(rule, run)} />}
    </div>
  );
}

function Picker({
  label,
  value,
  onChange,
  options,
  wide,
}: {
  label: string;
  value?: string;
  onChange: (v: string) => void;
  options: { value: string; label: string; hint?: string }[];
  wide?: boolean;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className={`${TOOLBAR_SELECT} ${wide ? 'sm:w-64' : 'sm:w-44'}`} aria-label={label}>
        <SelectValue placeholder={label} />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value} className="text-xs">
            <span className="font-mono">{o.label}</span>
            {o.hint && <span className="ml-1.5 text-muted-foreground">· {o.hint}</span>}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
