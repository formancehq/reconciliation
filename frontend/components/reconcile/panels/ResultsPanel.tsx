'use client';

/**
 * Results — reads the result files of transaction-level (lettering) rules in the
 * browser with DuckDB-WASM, running the SQL of tools/lettering-duckdb as it is.
 *
 * An internal reading and validation prototype: it reads the tool's test data
 * through a dev-only route (lib/lettering/source.ts). It does not replace the
 * production path, where the breaks and the statement come from recon's API.
 */
import { CalendarDays, FileText, FlaskConical, Files, TerminalSquare } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { FILTER_TOOLBAR } from '@/lib/uiClasses';
import { useReconResource } from '@/lib/recon';
import { testdataSource, type LetteringFile } from '@/lib/lettering/source';
import { readRuleRuns, rulesOf, type RuleRun } from '@/lib/lettering/read';
import { formatDay } from '@/lib/lettering/format';
import { useReconNav, type ResultsNav, type ResultsSection } from '../ReconContext';
import { EmptyState, ErrorState, Loading } from '../ui';
import { RunView } from '../results/RunView';
import { DaysView } from '../results/DaysView';
import { QueriesView } from '../results/QueriesView';

const source = testdataSource;

export function ResultsPanel() {
  const index = useReconResource<LetteringFile[]>(() => source.list(), []);
  if (index.loading) return <Loading label="Listing result files…" />;
  if (index.error) return <ErrorState error={index.error} onRetry={index.refetch} />;
  const files = index.data ?? [];
  if (files.length === 0) {
    return (
      <div className="p-6">
        <EmptyState icon={<Files className="h-7 w-7" />} title="No result files">
          The source lists no lettering result file.
        </EmptyState>
      </div>
    );
  }
  return <RuleResults files={files} />;
}

const SECTIONS: { id: ResultsSection; label: string; icon: typeof CalendarDays }[] = [
  { id: 'days', label: 'Days', icon: CalendarDays },
  { id: 'run', label: 'Run', icon: FileText },
  { id: 'queries', label: 'Queries', icon: TerminalSquare },
];

function RuleResults({ files }: { files: LetteringFile[] }) {
  const { nav, openResults } = useReconNav();
  const state = nav.results ?? {};
  const rules = rulesOf(files);
  const rule = state.rule && rules.includes(state.rule) ? state.rule : rules[0];
  const section = state.section ?? 'days';
  const go = (next: ResultsNav) => openResults({ ...state, rule, ...next });
  const runs = useReconResource<RuleRun[]>(() => readRuleRuns(files, rule), [files, rule]);

  return (
    <div className="mx-auto max-w-6xl min-w-0 space-y-4 p-3 sm:p-4">
      <div className={FILTER_TOOLBAR}>
        <Badge variant="amber" size="sm" title={`Source: ${source.label}`}>
          <FlaskConical className="h-3 w-3" /> Test data
        </Badge>
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

      {runs.loading ? (
        <Loading label="Starting DuckDB and reading the rule's manifests…" />
      ) : runs.error ? (
        <ErrorState error={runs.error} onRetry={runs.refetch} />
      ) : (runs.data ?? []).length === 0 ? (
        <EmptyState icon={<Files className="h-7 w-7" />} title="No run">
          This rule has no run.
        </EmptyState>
      ) : section === 'days' ? (
        <DaysView runs={runs.data ?? []} onOpen={(day, run) => go({ section: 'run', day, run })} />
      ) : section === 'run' ? (
        <RunSection files={files} rule={rule} runs={runs.data ?? []} state={state} go={go} />
      ) : (
        <QueriesView
          files={files}
          rule={rule}
          runs={runs.data ?? []}
          state={{ query: state.query, day: state.day, id: state.id }}
          onChange={(q) => go(q)}
        />
      )}
    </div>
  );
}

/** One run: the day's current run unless another is picked (slice 1's view). */
function RunSection({
  files,
  rule,
  runs,
  state,
  go,
}: {
  files: LetteringFile[];
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
      {run && day && <RunView key={`${rule}/${run.run}`} path={`rule=${rule}/day=${day}/run=${run.run}`} deps={[files]} />}
    </div>
  );
}

function Picker({
  label,
  value,
  onChange,
  options,
  disabled,
  wide,
}: {
  label: string;
  value?: string;
  onChange: (v: string) => void;
  options: { value: string; label: string; hint?: string }[];
  disabled?: boolean;
  wide?: boolean;
}) {
  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger className={`h-8 w-full text-xs ${wide ? 'sm:w-64' : 'sm:w-44'}`} aria-label={label}>
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
