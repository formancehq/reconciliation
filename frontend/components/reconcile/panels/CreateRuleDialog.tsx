'use client';

/**
 * Template-driven "create rule" wizard.
 *
 * The form is driven by the selected `templateKind` (RECON-API.md /
 * TEMPLATE-SPECS.md): each kind renders its own spec editor (terms / sources /
 * bounds). Amounts are integers in the asset's minor units; a `query` is built
 * from an address selector into the go-libs `$match` DSL (trailing `*` = prefix
 * match). On a 400 the server names the offending field — we surface it inline.
 */
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle, ArrowLeftRight, CalendarClock, Check, ChevronRight,
  Gauge, Loader2, MousePointerClick, Plus, Scale, Trash2, Wand2,
} from 'lucide-react';
import { cn } from '@workspace/ui/lib/utils';
import { useLedgerClient } from '@/lib/connection/provider';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from '@/components/ui/select';
import { toast } from '@/components/ui/toast';
import { ToggleGroup, ToggleGroupItem } from '@workspace/ui/components/toggle-group';
import { RadioGroup, RadioGroupItem } from '@workspace/ui/components/radio-group';
import {
  reconClient, ReconError, TEMPLATE_META, TEMPLATE_KINDS, SEVERITY_ORDER, SEVERITY_META, CADENCE_META,
  buildParitySide, parityScope, parseParitySide, isParitySideComplete, validateParitySide, validateParityPair, PARITY_SOURCE_KINDS,
  type Rule, type RuleRequest, type TemplateKind, type Severity, type Cadence, type Schedule, type ParitySourceKind,
} from '@/lib/recon';
import createLogger from '@/lib/logger';
import { useChartStore } from '@/stores/chartStore';
import {
  MetaFilterBuilder, compileReconQuery, parseReconQuery,
  type MetaRule, type MetaCombinator,
} from '../MetaFilterBuilder';
import { SourceParityComparison } from '../SourceParityComparison';
import { AccountSelectorInput } from '../AccountSelectorInput';
import { LedgerCombobox } from '../LedgerCombobox';

const log = createLogger('Recon');

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** When set, the dialog edits this rule (PATCH) instead of creating one. */
  editRule?: Rule | null;
  /** When set (and not editing), prefill a new rule from this one (create/POST). */
  duplicateRule?: Rule | null;
  onSaved: (rule: Rule) => void;
}

type Sign = 1 | -1;
/** A query = an address selector + typed metadata conditions (see MetaFilterBuilder). */
interface Query { address: string; meta: MetaRule[]; metaComb: MetaCombinator }
interface Term extends Query { ledger: string; sign: Sign }
interface Amount { asset: string; amount: string }
interface Bound { asset: string; min: string; max: string }
/** A source_parity side: a ledger balance, or one metadata key representing one asset. */
interface Source extends Query {
  ledger: string; kind: ParitySourceKind;
  metadataKey: string; asset: string;
}
type Mode = 'aggregate' | 'per_account';

const CADENCES: Cadence[] = ['continuous', 'daily', 'weekly', 'monthly'];
const TEMPLATE_CHOICES: Record<TemplateKind, {
  capability: string;
  icon: typeof Scale;
}> = {
  ledger_invariant: {
    capability: 'Assign debit-normal or credit-normal orientation to ledger account sets, then check that their normalized total stays within tolerance.',
    icon: Scale,
  },
  source_parity: {
    capability: 'Compare any two posting-derived or account-metadata balances, asset by asset, with an optional tolerance.',
    icon: ArrowLeftRight,
  },
  account_threshold: {
    capability: 'Select ledger accounts and define minimum and/or maximum balances for each asset.',
    icon: Gauge,
  },
};

const CADENCE_DESCRIPTIONS: Record<Cadence, string> = {
  continuous: 'One ongoing alert thread',
  daily: 'One alert thread per day',
  weekly: 'One alert thread per week',
  monthly: 'One alert thread per month',
};

const ALERT_PERIOD_COPY: Record<Cadence, {
  activeTitle: string;
  activeDescription: string;
  rolloverTitle: string;
  rolloverDescription: string;
}> = {
  continuous: {
    activeTitle: 'One ongoing alert',
    activeDescription: 'Matching observations return to the same alert, which may be resolved and reopened.',
    rolloverTitle: 'No period rollover',
    rolloverDescription: 'Continuous monitoring keeps the same alert thread across time.',
  },
  daily: {
    activeTitle: 'One alert this day',
    activeDescription: 'It collects matching observations and may be resolved and reopened during the day.',
    rolloverTitle: 'Next day',
    rolloverDescription: 'The next matching failure starts a new alert for the new day.',
  },
  weekly: {
    activeTitle: 'One alert this week',
    activeDescription: 'It collects matching observations and may be resolved and reopened during the week.',
    rolloverTitle: 'Next week',
    rolloverDescription: 'The next matching failure starts a new alert for the new week.',
  },
  monthly: {
    activeTitle: 'One alert this month',
    activeDescription: 'It collects matching observations and may be resolved and reopened during the month.',
    rolloverTitle: 'Next month',
    rolloverDescription: 'The next matching failure starts a new alert for the new month.',
  },
};

const DEFAULT_CRON: Record<Cadence, string> = {
  continuous: '*/15 * * * *',
  daily: '0 0 * * *',
  weekly: '0 0 * * 1',
  monthly: '0 0 1 * *',
};

const CRON_PRESETS = [
  { expr: '*/15 * * * *', label: 'Every 15 minutes' },
  { expr: '0 * * * *', label: 'Hourly' },
  { expr: '0 0 * * *', label: 'Daily at 00:00' },
  { expr: '0 0 * * 1', label: 'Weekly on Monday' },
  { expr: '0 0 1 * *', label: 'Monthly on the 1st' },
] as const;
const SCOPE_OPTIONS: { value: Mode; label: string }[] = [
  { value: 'aggregate', label: 'Aggregate' },
  { value: 'per_account', label: 'Per account' },
];
const emptyQuery = (address = ''): Query => ({ address, meta: [], metaComb: 'and' });

interface FormState {
  name: string; kind: TemplateKind; severity: Severity; cadence: Cadence; enabled: boolean; schedule: Schedule;
  terms: Term[]; invTol: Amount[];
  left: Source; right: Source; scope: Mode; parTol: Amount[];
  thLedger: string; thQuery: Query; thMode: Mode; bounds: Bound[];
}

const DEFAULT_STATE: FormState = {
  name: '', kind: 'source_parity', severity: 'high', cadence: 'continuous', enabled: true, schedule: { kind: 'on_demand' },
  terms: [{ ledger: '', sign: 1, ...emptyQuery() }], invTol: [{ asset: 'USD', amount: '0' }],
  left: { ledger: '', kind: 'ledger', metadataKey: '', asset: '', ...emptyQuery() },
  right: { ledger: '', kind: 'ledger', metadataKey: '', asset: '', ...emptyQuery() },
  scope: 'aggregate', parTol: [{ asset: 'USD', amount: '0' }],
  thLedger: '', thQuery: emptyQuery(), thMode: 'aggregate', bounds: [{ asset: 'USD', min: '', max: '' }],
};

const tolRows = (t: unknown): Amount[] => {
  const rows = Object.entries((t && typeof t === 'object' ? t : {}) as Record<string, unknown>).map(([asset, amount]) => ({ asset, amount: String(amount) }));
  return rows.length ? rows : [{ asset: 'USD', amount: '0' }];
};
const metadataTolRows = (t: unknown, asset: string): Amount[] => {
  const values = (t && typeof t === 'object' ? t : {}) as Record<string, unknown>;
  return [{ asset, amount: String(values[asset] ?? 0) }];
};
const boundRows = (b: unknown): Bound[] => {
  const rows = Object.entries((b && typeof b === 'object' ? b : {}) as Record<string, { min?: number; max?: number }>)
    .map(([asset, v]) => ({ asset, min: v?.min !== undefined ? String(v.min) : '', max: v?.max !== undefined ? String(v.max) : '' }));
  return rows.length ? rows : [{ asset: 'USD', min: '', max: '' }];
};

/** Reverse-map a rule into editor state (inverse of buildSpec) for edit mode. */
function buildInitialState(rule?: Rule | null): FormState {
  if (!rule) return DEFAULT_STATE;
  const base: FormState = { ...DEFAULT_STATE, name: rule.name, kind: rule.templateKind, severity: rule.severity, cadence: rule.cadence, enabled: rule.enabled, schedule: rule.schedule ?? { kind: 'on_demand' } };
  const spec = (rule.templateSpec ?? {}) as Record<string, unknown>;
  if (rule.templateKind === 'ledger_invariant') {
    const terms = (Array.isArray(spec.terms) ? (spec.terms as Array<Record<string, unknown>>) : [])
      .map((t) => ({ ledger: String(t.ledger ?? ''), sign: (Number(t.sign) < 0 ? -1 : 1) as Sign, ...parseReconQuery(t.query) }));
    return { ...base, terms: terms.length ? terms : DEFAULT_STATE.terms, invTol: tolRows(spec.tolerance) };
  }
  if (rule.templateKind === 'source_parity') {
    const l = (spec.left ?? {}) as Record<string, unknown>;
    const r = (spec.right ?? {}) as Record<string, unknown>;
    const lk = parseParitySide(l);
    const rk = parseParitySide(r);
    return {
      ...base,
      left: { ledger: String(l.ledger ?? ''), kind: lk.kind, metadataKey: lk.metadataKey, asset: lk.asset, ...parseReconQuery(l.query) },
      right: { ledger: String(r.ledger ?? ''), kind: rk.kind, metadataKey: rk.metadataKey, asset: rk.asset, ...parseReconQuery(r.query) },
      scope: spec.scope === 'per_account' ? 'per_account' : 'aggregate',
      parTol: lk.kind === 'account_metadata' || rk.kind === 'account_metadata'
        ? metadataTolRows(spec.tolerance, lk.kind === 'account_metadata' ? lk.asset : rk.asset)
        : tolRows(spec.tolerance),
    };
  }
  return {
    ...base,
    thLedger: String(spec.ledger ?? ''),
    thQuery: parseReconQuery(spec.query),
    thMode: spec.mode === 'per_account' ? 'per_account' : 'aggregate',
    bounds: boundRows(spec.bounds),
  };
}

export function nextScheduleForCadence(schedule: Schedule, current: Cadence, next: Cadence): Schedule {
  if (schedule.kind !== 'cron') return schedule;
  const expr = (schedule.expr ?? '').trim();
  if (expr && expr !== DEFAULT_CRON[current]) return schedule;
  return { ...schedule, expr: DEFAULT_CRON[next] };
}

function scheduleDescription(schedule: Schedule): string {
  if (schedule.kind === 'on_demand') return 'Runs only when someone chooses Run now.';
  const preset = CRON_PRESETS.find(({ expr }) => expr === schedule.expr);
  return preset
    ? `Runs automatically: ${preset.label.toLowerCase()} (${schedule.tz?.trim() || 'UTC'}).`
    : `Runs automatically on the custom cron schedule (${schedule.tz?.trim() || 'UTC'}).`;
}

export function TemplatePicker({ value, onChange }: {
  value: TemplateKind;
  onChange: (value: TemplateKind) => void;
}) {
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium">What should this rule check?</legend>
      <RadioGroup
        value={value}
        onValueChange={(next) => onChange(next as TemplateKind)}
        className="grid gap-2 md:grid-cols-3"
        aria-label="Rule template"
      >
        {TEMPLATE_KINDS.map((template) => {
          const selected = template === value;
          const Icon = TEMPLATE_CHOICES[template].icon;
          return (
            <label
              key={template}
              className={cn(
                'relative flex min-h-36 cursor-pointer flex-col gap-3 rounded-md border bg-background p-4 transition-colors',
                'hover:border-foreground/30 hover:bg-muted/30',
                'focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30',
                selected && 'border-primary bg-primary/5 ring-1 ring-primary/20',
              )}
            >
              <div className="flex items-start justify-between gap-3">
                <span className={cn('flex size-9 items-center justify-center rounded-sm border bg-muted/40', selected && 'border-primary/30 bg-primary/10 text-primary')}>
                  <Icon className="size-4" aria-hidden="true" />
                </span>
                <RadioGroupItem value={template} aria-label={TEMPLATE_META[template].label} />
              </div>
              <span className="space-y-1">
                <span className="block text-sm font-semibold">{TEMPLATE_META[template].label}</span>
                <span className="block text-xs leading-relaxed text-muted-foreground">
                  {TEMPLATE_CHOICES[template].capability}
                </span>
              </span>
            </label>
          );
        })}
      </RadioGroup>
    </fieldset>
  );
}

export function RunTimingFields({ cadence, schedule, cadenceLocked = false, onCadenceChange, onScheduleChange }: {
  cadence: Cadence;
  schedule: Schedule;
  cadenceLocked?: boolean;
  onCadenceChange: (value: Cadence) => void;
  onScheduleChange: (value: Schedule) => void;
}) {
  const periodCopy = ALERT_PERIOD_COPY[cadence];
  return (
    <fieldset className="space-y-4 rounded-md border bg-muted/15 p-4">
      <legend className="text-sm font-semibold">When should it evaluate?</legend>
      <p className="-mt-3 text-xs leading-relaxed text-muted-foreground">
        Alert period groups related failing observations into alert threads. Run mode separately controls when evaluations happen.
      </p>

      <div className="space-y-2">
        <div className="flex items-baseline justify-between gap-3">
          <Label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Alert grouping period</Label>
          {cadenceLocked && <span className="text-xs text-muted-foreground">Fixed after creation</span>}
        </div>
        <RadioGroup
          value={cadence}
          onValueChange={(next) => onCadenceChange(next as Cadence)}
          disabled={cadenceLocked}
          className="grid gap-2 sm:grid-cols-2 md:grid-cols-4"
          aria-label="Alert grouping period"
        >
          {CADENCES.map((option) => (
            <label
              key={option}
              className={cn(
                'flex min-h-16 cursor-pointer items-start gap-2.5 rounded-md border bg-background p-3 transition-colors',
                'hover:bg-muted/30 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30',
                cadence === option && 'border-primary bg-primary/5 ring-1 ring-primary/20',
                cadenceLocked && 'cursor-not-allowed opacity-60',
              )}
            >
              <RadioGroupItem value={option} className="mt-0.5" aria-label={CADENCE_META[option].label} />
              <span>
                <span className="block text-sm font-medium">{CADENCE_META[option].label}</span>
                <span className="mt-0.5 block text-xs leading-snug text-muted-foreground">{CADENCE_DESCRIPTIONS[option]}</span>
              </span>
            </label>
          ))}
        </RadioGroup>
      </div>

      <div className="space-y-2 rounded-md border bg-background p-3" role="note" aria-label={`${CADENCE_META[cadence].label} alert lifecycle`}>
        <div>
          <p className="text-xs font-semibold text-foreground">How the {CADENCE_META[cadence].label.toLowerCase()} period works</p>
          <p className="mt-0.5 text-xs text-muted-foreground">Related failures share one alert only while the same period is active.</p>
        </div>
        <div className="grid items-stretch gap-2 md:grid-cols-[1fr_auto_1fr_auto_1fr]">
          <div className="rounded-sm bg-muted/35 p-2.5">
            <p className="text-xs font-medium text-foreground">Failed observations arrive</p>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">Each matching failed evaluation records another observation.</p>
          </div>
          <ChevronRight className="mx-auto size-4 self-center rotate-90 text-muted-foreground md:rotate-0" aria-hidden="true" />
          <div className="rounded-sm border border-primary/20 bg-primary/5 p-2.5">
            <p className="text-xs font-medium text-foreground">{periodCopy.activeTitle}</p>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{periodCopy.activeDescription}</p>
          </div>
          <ChevronRight className="mx-auto size-4 self-center rotate-90 text-muted-foreground md:rotate-0" aria-hidden="true" />
          <div className="rounded-sm bg-muted/35 p-2.5">
            <p className="text-xs font-medium text-foreground">{periodCopy.rolloverTitle}</p>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{periodCopy.rolloverDescription}</p>
          </div>
        </div>
      </div>

      <div className="space-y-2">
        <Label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Run mode</Label>
        <RadioGroup
          value={schedule.kind}
          onValueChange={(next) => onScheduleChange(
            next === 'cron'
              ? { kind: 'cron', expr: schedule.expr || DEFAULT_CRON[cadence], tz: schedule.tz }
              : { kind: 'on_demand' },
          )}
          className="grid gap-2 sm:grid-cols-2"
          aria-label="Run mode"
        >
          <label className={cn(
            'flex cursor-pointer items-start gap-3 rounded-md border bg-background p-3 transition-colors hover:bg-muted/30 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30',
            schedule.kind === 'on_demand' && 'border-primary bg-primary/5 ring-1 ring-primary/20',
          )}>
            <MousePointerClick className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">On demand</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">Evaluate only when Run now is used.</span>
            </span>
            <RadioGroupItem value="on_demand" aria-label="On demand" />
          </label>
          <label className={cn(
            'flex cursor-pointer items-start gap-3 rounded-md border bg-background p-3 transition-colors hover:bg-muted/30 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30',
            schedule.kind === 'cron' && 'border-primary bg-primary/5 ring-1 ring-primary/20',
          )}>
            <CalendarClock className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">Automatic schedule</span>
              <span className="mt-0.5 block text-xs text-muted-foreground">Evaluate automatically using a preset or cron.</span>
            </span>
            <RadioGroupItem value="cron" aria-label="Automatic schedule" />
          </label>
        </RadioGroup>
      </div>

      {schedule.kind === 'cron' && (
        <div className="space-y-3 rounded-md border bg-background p-3">
          <div className="flex flex-wrap gap-1.5" aria-label="Schedule presets">
            {CRON_PRESETS.map(({ expr, label }) => (
              <Button
                key={expr}
                type="button"
                size="sm"
                variant="outline"
                className={cn('h-7 text-xs', schedule.expr === expr && 'border-primary bg-primary/5')}
                onClick={() => onScheduleChange({ ...schedule, expr })}
                aria-pressed={schedule.expr === expr}
              >
                {schedule.expr === expr && <Check className="mr-1 size-3" aria-hidden="true" />}
                {label}
              </Button>
            ))}
          </div>
          <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_10rem]">
            <Field label="Cron expression" htmlFor="rule-cron-expression">
              <Input
                id="rule-cron-expression"
                className="font-mono"
                value={schedule.expr ?? ''}
                onChange={(event) => onScheduleChange({ ...schedule, expr: event.target.value })}
                placeholder={DEFAULT_CRON[cadence]}
              />
            </Field>
            <Field label="Time zone" htmlFor="rule-time-zone">
              <Input
                id="rule-time-zone"
                value={schedule.tz ?? ''}
                onChange={(event) => onScheduleChange({ ...schedule, tz: event.target.value })}
                placeholder="UTC"
              />
            </Field>
          </div>
        </div>
      )}

      <p className="flex items-start gap-2 border-t pt-3 text-xs text-muted-foreground" role="status">
        <Check className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden="true" />
        <span><strong className="font-medium text-foreground">{CADENCE_META[cadence].label} alert period.</strong> {CADENCE_DESCRIPTIONS[cadence]}. {scheduleDescription(schedule)}</span>
      </p>
    </fieldset>
  );
}

export function CreateRuleDialog({ open, onOpenChange, editRule, duplicateRule, onSaved }: Props) {
  const isEdit = !!editRule;
  const isDup = !editRule && !!duplicateRule;
  // The seed rule is stable for this instance's life (the parent keys the dialog
  // by rule id), so the initial state is computed once. Duplicating prefills from
  // the source rule but stays a create (POST), with a distinct name.
  const init = useMemo(() => {
    const s = buildInitialState(editRule ?? duplicateRule);
    return isDup ? { ...s, name: s.name ? `${s.name} (copy)` : '' } : s;
  }, [editRule, duplicateRule, isDup]);

  const [name, setName] = useState(init.name);
  const [kind, setKind] = useState<TemplateKind>(init.kind);
  const [severity, setSeverity] = useState<Severity>(init.severity);
  const [cadence, setCadence] = useState<Cadence>(init.cadence);
  const [enabled, setEnabled] = useState(init.enabled);
  const [schedule, setSchedule] = useState<Schedule>(init.schedule);

  const changeCadence = (next: Cadence) => {
    setSchedule((currentSchedule) => nextScheduleForCadence(currentSchedule, cadence, next));
    setCadence(next);
  };

  // Per-template spec state (only the active kind is serialized on submit).
  const [terms, setTerms] = useState<Term[]>(init.terms);
  const [invTol, setInvTol] = useState<Amount[]>(init.invTol);
  const [left, setLeft] = useState<Source>(init.left);
  const [right, setRight] = useState<Source>(init.right);
  const [scope, setScope] = useState<Mode>(init.scope);
  const [parTol, setParTol] = useState<Amount[]>(init.parTol);
  const [thLedger, setThLedger] = useState(init.thLedger);
  const [thQuery, setThQuery] = useState<Query>(init.thQuery);
  const [thMode, setThMode] = useState<Mode>(init.thMode);
  const [bounds, setBounds] = useState<Bound[]>(init.bounds);

  const [submitting, setSubmitting] = useState(false);
  const [serverError, setServerError] = useState<ReconError | null>(null);
  const activeLedger = useChartStore((state) => state.selectedLedger);

  // Ledger name suggestions from the connected V3 ledger (if any). Recon is
  // connection-independent, so this is a best-effort convenience: the fields
  // stay free-text (you can name a ledger the app isn't connected to).
  const ledgerClient = useLedgerClient();
  const [ledgerOptionState, setLedgerOptionState] = useState<{ client: typeof ledgerClient; values: string[] }>({ client: null, values: [] });
  const ledgerOptions = ledgerOptionState.client === ledgerClient ? ledgerOptionState.values : [];
  useEffect(() => {
    if (!open || !ledgerClient) return;
    let alive = true;
    ledgerClient.listLedgers()
      .then((ls) => { if (alive) setLedgerOptionState({ client: ledgerClient, values: ls.map((l) => l.name) }); })
      .catch((err) => { log.debug('listLedgers for suggestions failed — leaving free-text', { err }); });
    return () => { alive = false; };
  }, [open, ledgerClient]);

  // The browser's active Explorer ledger is only a create-time default. Each
  // source remains independent after seeding, and saved edit/duplicate values win.
  const seededActiveLedger = useRef(false);
  useEffect(() => {
    if (!open) { seededActiveLedger.current = false; return; }
    if (seededActiveLedger.current || isEdit || isDup || !activeLedger.trim()) return;
    const ledger = activeLedger.trim();
    setTerms((current) => current.map((term) => term.ledger.trim() ? term : { ...term, ledger }));
    setLeft((current) => current.ledger.trim() ? current : { ...current, ledger });
    setRight((current) => current.ledger.trim() ? current : { ...current, ledger });
    setThLedger((current) => current.trim() ? current : ledger);
    seededActiveLedger.current = true;
  }, [open, isEdit, isDup, activeLedger]);

  const reset = () => {
    setName(init.name); setKind(init.kind); setSeverity(init.severity); setCadence(init.cadence); setEnabled(init.enabled); setSchedule(init.schedule);
    setTerms(init.terms); setInvTol(init.invTol);
    setLeft(init.left); setRight(init.right); setScope(init.scope); setParTol(init.parTol);
    setThLedger(init.thLedger); setThQuery(init.thQuery); setThMode(init.thMode); setBounds(init.bounds);
    setServerError(null);
  };

  const close = (o: boolean) => { onOpenChange(o); if (!o) { reset(); setSubmitting(false); } };

  const loadExample = () => {
    setServerError(null);
    // Only suggest a name when the field is still empty — never clobber a name
    // the user has already typed.
    const suggestName = (n: string) => { if (!name.trim()) setName(n); };
    if (kind === 'ledger_invariant') {
      const ledger = terms[0]?.ledger.trim() || activeLedger.trim() || 'treasury';
      suggestName('Balance sheet nets to zero');
      setTerms([
        { ledger, sign: -1, ...emptyQuery('assets:*') },
        { ledger, sign: 1, ...emptyQuery('liabilities:*') },
      ]);
      setInvTol([{ asset: 'USD', amount: '0' }]);
    } else if (kind === 'source_parity') {
      // Demonstrate that the reconciled metadata key and represented asset are independent.
      const leftLedger = left.ledger.trim() || activeLedger.trim() || 'book';
      const rightLedger = right.ledger.trim() || activeLedger.trim() || 'book';
      suggestName('Custody parity — ledger vs synced metadata');
      setLeft({ ledger: leftLedger, kind: 'ledger', metadataKey: '', asset: '', ...emptyQuery('cash:custody') });
      setRight({ ledger: rightLedger, kind: 'account_metadata', metadataKey: 'value_known.toto', asset: 'USD/2', ...emptyQuery('mirror:custody') });
      setScope('aggregate');
      setParTol([{ asset: 'USD/2', amount: '0' }]);
    } else {
      suggestName('USD reserve floor');
      setThLedger(thLedger.trim() || activeLedger.trim() || 'treasury'); setThQuery(emptyQuery('reserves:usd:*')); setThMode('aggregate');
      setBounds([{ asset: 'USD', min: '1000000', max: '' }]);
    }
  };

  const toToleranceMap = (rows: Amount[]): Record<string, number> => {
    const m: Record<string, number> = {};
    for (const r of rows) if (r.asset.trim()) m[r.asset.trim()] = Number(r.amount || '0');
    return m;
  };

  const buildSpec = (): Record<string, unknown> => {
    switch (kind) {
      case 'ledger_invariant':
        return {
          terms: terms.map((t) => ({ ledger: t.ledger.trim(), query: compileReconQuery(t.address, t.meta, t.metaComb), sign: t.sign })),
          tolerance: toToleranceMap(invTol),
        };
      case 'source_parity': {
        const metadataAsset = left.kind === 'account_metadata' ? left.asset.trim() : right.kind === 'account_metadata' ? right.asset.trim() : '';
        const tol = metadataAsset
          ? { [metadataAsset]: Number(parTol.find((row) => row.asset.trim() === metadataAsset)?.amount || '0') }
          : toToleranceMap(parTol);
        const side = (s: Source) => buildParitySide({
          kind: s.kind,
          ledger: s.ledger.trim(),
          query: compileReconQuery(s.address, s.meta, s.metaComb),
          metadataKey: s.metadataKey,
          asset: s.asset,
        });
        return {
          left: side(left),
          right: side(right),
          // account_metadata is aggregate-only — coerce so we never send an
          // invalid per_account+metadata combination the server would 400.
          scope: parityScope([left.kind, right.kind], scope),
          ...(Object.keys(tol).length ? { tolerance: tol } : {}),
        };
      }
      case 'account_threshold': {
        const b: Record<string, { min?: number; max?: number }> = {};
        for (const row of bounds) {
          if (!row.asset.trim()) continue;
          const entry: { min?: number; max?: number } = {};
          if (row.min.trim() !== '') entry.min = Number(row.min);
          if (row.max.trim() !== '') entry.max = Number(row.max);
          if (entry.min !== undefined || entry.max !== undefined) b[row.asset.trim()] = entry;
        }
        return { ledger: thLedger.trim(), query: compileReconQuery(thQuery.address, thQuery.meta, thQuery.metaComb), mode: thMode, bounds: b };
      }
    }
  };

  // Lightweight client validation (server is the source of truth; this just
  // prevents obviously-empty submits and enables the button).
  const validationErrors = useMemo(() => {
    if (kind !== 'source_parity') return [];
    return [
      ...validateParitySide(left),
      ...validateParitySide(right),
      ...validateParityPair(left, right),
    ];
  }, [kind, left, right]);

  const valid = useMemo(() => {
    if (!name.trim()) return false;
    if (schedule.kind === 'cron' && !(schedule.expr ?? '').trim()) return false;
    if (kind === 'ledger_invariant') {
      return terms.some((t) => t.ledger.trim()) && invTol.some((t) => t.asset.trim());
    }
    if (kind === 'source_parity') {
      if (!left.ledger.trim() || !right.ledger.trim()) return false;
      return isParitySideComplete(left) && isParitySideComplete(right) && validationErrors.length === 0;
    }
    if (kind === 'account_threshold') {
      return !!thLedger.trim() && bounds.some((b) => b.asset.trim() && (b.min.trim() !== '' || b.max.trim() !== ''));
    }
    return false;
  }, [name, kind, terms, invTol, left, right, thLedger, bounds, schedule, validationErrors]);

  const submit = async () => {
    setSubmitting(true);
    setServerError(null);
    const sched: Schedule = schedule.kind === 'cron'
      ? { kind: 'cron', expr: (schedule.expr ?? '').trim(), ...(schedule.tz?.trim() ? { tz: schedule.tz.trim() } : {}) }
      : { kind: 'on_demand' };
    // cadence is only settable at create — PATCH doesn't accept it (compiled CEL
    // is rederived from the spec server-side). schedule IS patchable.
    const common = { name: name.trim(), templateKind: kind, templateSpec: buildSpec(), severity, enabled, schedule: sched };
    try {
      const rule = isEdit && editRule
        ? await reconClient.patchRule(editRule.id, common)
        : await reconClient.createRule({ ...common, cadence } as RuleRequest);
      toast.success(isEdit ? 'Rule updated' : isDup ? 'Rule duplicated' : 'Rule created', { description: rule.name });
      onSaved(rule);
      close(false);
    } catch (err) {
      log.error(isEdit ? 'patchRule failed' : 'createRule failed', { name: common.name, templateKind: common.templateKind, error: err instanceof ReconError ? `${err.errorCode}: ${err.message}` : err instanceof Error ? err.message : String(err) });
      if (err instanceof ReconError) setServerError(err);
      else toast.error(isEdit ? 'Failed to update rule' : 'Failed to create rule');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="max-h-[92vh] gap-0 overflow-hidden p-0 sm:max-w-4xl">
        <DialogHeader className="border-b px-6 py-4">
          <DialogTitle>{isEdit ? 'Edit rule' : isDup ? 'Duplicate rule' : 'New reconciliation rule'}</DialogTitle>
          <DialogDescription>{TEMPLATE_META[kind].blurb}</DialogDescription>
        </DialogHeader>

        <div className="max-h-[calc(92vh-9rem)] space-y-5 overflow-y-auto px-6 py-5">
          {/* Common */}
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_14rem]">
            <Field label="Name" htmlFor="rule-name">
              <Input id="rule-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. USD source parity — treasury vs bank" />
            </Field>
            <Field label="Severity" htmlFor="rule-severity">
              <Select value={severity} onValueChange={(v) => setSeverity(v as Severity)}>
                <SelectTrigger id="rule-severity"><SelectValue /></SelectTrigger>
                <SelectContent className="[&_[data-slot=select-scroll-down-button]]:hidden [&_[data-slot=select-scroll-up-button]]:hidden">
                  {SEVERITY_ORDER.map((s) => <SelectItem key={s} value={s}>{SEVERITY_META[s].label}</SelectItem>)}
                </SelectContent>
              </Select>
            </Field>
          </div>

          <TemplatePicker value={kind} onChange={(next) => { setKind(next); setServerError(null); }} />

          <RunTimingFields
            cadence={cadence}
            schedule={schedule}
            cadenceLocked={isEdit}
            onCadenceChange={changeCadence}
            onScheduleChange={setSchedule}
          />

          {/* Template-specific spec editor */}
          <Card>
            <div className="flex items-center justify-between border-b bg-muted/40 px-3 py-2">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {TEMPLATE_META[kind].label} configuration
              </span>
              {!isEdit && !isDup && (
                <Button type="button" size="sm" variant="ghost" onClick={loadExample}>
                  <Wand2 className="mr-1.5 h-3.5 w-3.5" /> Load example
                </Button>
              )}
            </div>
            <div className="space-y-4 p-3">
              {kind === 'ledger_invariant' && (
                <InvariantEditor terms={terms} setTerms={setTerms} tol={invTol} setTol={setInvTol} ledgerOptions={ledgerOptions} defaultLedger={activeLedger} />
              )}
              {kind === 'source_parity' && (
                <ParityEditor left={left} setLeft={setLeft} right={right} setRight={setRight} scope={scope} setScope={setScope} tol={parTol} setTol={setParTol} ledgerOptions={ledgerOptions} />
              )}
              {kind === 'account_threshold' && (
                <ThresholdEditor ledger={thLedger} setLedger={setThLedger} query={thQuery} setQuery={setThQuery} mode={thMode} setMode={setThMode} bounds={bounds} setBounds={setBounds} ledgerOptions={ledgerOptions} />
              )}
            </div>
          </Card>

          {isEdit && editRule?.compiledCEL && (
            <div className="space-y-1">
              <Label className="text-sm">Compiled CEL <span className="font-normal text-muted-foreground">— derived from the configuration (read-only)</span></Label>
              <p className="rounded-md border bg-muted/40 px-3 py-2 font-mono text-xs break-all text-muted-foreground">{editRule.compiledCEL}</p>
            </div>
          )}

          <div className="flex items-center justify-between rounded-lg border px-3 py-2">
            <div>
              <Label className="text-sm">Enabled</Label>
              <p className="text-xs text-muted-foreground">{isEdit ? 'Turn monitoring on or off.' : 'Start observing as soon as it’s created.'}</p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </div>

          {serverError && (
            <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
              <div>
                <p className="font-medium">{serverError.errorCode}: {serverError.message}</p>
                {serverError.details && <p className="font-mono text-xs opacity-80">{serverError.details}</p>}
              </div>
            </div>
          )}
        </div>

        <DialogFooter className="border-t px-6 py-4">
          <Button variant="ghost" onClick={() => close(false)} disabled={submitting}>Cancel</Button>
          <Button onClick={submit} disabled={!valid || submitting}>
            {submitting && <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />}
            {isEdit ? 'Save changes' : 'Create rule'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Field wrapper ─────────────────────────────────────────────────────────
function Field({ label, hint, htmlFor, children }: { label: string; hint?: string; htmlFor?: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex items-baseline justify-between">
        <Label className="text-sm" htmlFor={htmlFor}>{label}</Label>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

// ── Segmented control (2–3 mutually-exclusive options) ──────────────────────
function Segmented<T extends string>({ value, onChange, options, ariaLabel, disabledValues = [], className }: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
  ariaLabel?: string;
  disabledValues?: T[];
  className?: string;
}) {
  return (
    <ToggleGroup
      type="single"
      value={value}
      onValueChange={(next) => { if (next) onChange(next as T); }}
      aria-label={ariaLabel}
      variant="default"
      size="md"
      className={cn('max-w-full overflow-hidden border border-input bg-background shadow-xs', className)}
    >
      {options.map((o) => (
        <ToggleGroupItem
          key={o.value}
          value={o.value}
          disabled={disabledValues.includes(o.value)}
          className={cn(
            'whitespace-nowrap border-r border-input bg-background text-xs text-muted-foreground last:border-r-0',
            'hover:bg-muted/50 hover:text-foreground',
            'data-[state=on]:bg-primary/10 data-[state=on]:text-primary',
          )}
        >
          {value === o.value && <Check className="size-3.5" aria-hidden="true" />}
          {o.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

const MINOR_UNITS_HINT = 'Integer, in the asset’s minor units (e.g. cents). 0 = strict.';

// ── Amount (asset → integer) rows, shared by tolerance editors ──────────────
function AmountRows({ rows, setRows, addLabel }: { rows: Amount[]; setRows: (r: Amount[]) => void; addLabel: string }) {
  const update = (i: number, patch: Partial<Amount>) => setRows(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  return (
    <div className="space-y-2">
      {rows.map((r, i) => (
        <div key={i} className="flex flex-wrap items-center gap-2">
          <Input className="w-24" value={r.asset} onChange={(e) => update(i, { asset: e.target.value })} placeholder="USD" />
          <Input className="min-w-36 flex-1" inputMode="numeric" value={r.amount} onChange={(e) => update(i, { amount: e.target.value })} placeholder="0" />
          <Button type="button" size="icon-sm" variant="ghost" onClick={() => setRows(rows.filter((_, idx) => idx !== i))} disabled={rows.length === 1} aria-label="Remove">
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      ))}
      <Button type="button" size="sm" variant="outline" onClick={() => setRows([...rows, { asset: '', amount: '0' }])}>
        <Plus className="mr-1.5 h-3.5 w-3.5" /> {addLabel}
      </Button>
    </div>
  );
}

// ── Metadata filter block: a labeled MetaFilterBuilder for one query ────────
// `collapsible` hides the builder behind a disclosure (most sources are
// address-only) — it starts open when a filter already exists (edit mode).
function MetaBlock({ ledger, query, setQuery, collapsible = false }: {
  ledger: string; query: Query; setQuery: (q: Query) => void; collapsible?: boolean;
}) {
  const [open, setOpen] = useState(!collapsible || query.meta.length > 0);
  const builder = (
    <>
      <MetaFilterBuilder
        ledger={ledger}
        rules={query.meta}
        setRules={(m) => setQuery({ ...query, meta: m })}
        combinator={query.metaComb}
        setCombinator={(c) => setQuery({ ...query, metaComb: c })}
      />
    </>
  );

  if (!collapsible) {
    return (
      <div className="space-y-2">
        <div className="space-y-1">
          <span className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Metadata filter (optional)</span>
          {builder}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground transition-colors hover:text-foreground"
        aria-expanded={open}
      >
        <ChevronRight className={cn('h-3.5 w-3.5 transition-transform', open && 'rotate-90')} />
        Filter accounts (optional){query.meta.length > 0 ? ` · ${query.meta.length}` : ''}
      </button>
      {open && <div className="space-y-2">{builder}</div>}
    </div>
  );
}

// ── ledger_invariant ────────────────────────────────────────────────────────
export function NormalBalanceControl({ sign, onChange, ariaLabel = 'Normal balance' }: {
  sign: Sign;
  onChange: (sign: Sign) => void;
  ariaLabel?: string;
}) {
  return (
    <Segmented
      ariaLabel={ariaLabel}
      className="w-full"
      value={String(sign) as '-1' | '1'}
      onChange={(value) => onChange(Number(value) as Sign)}
      options={[
        { value: '-1', label: 'Debit-normal (−)' },
        { value: '1', label: 'Credit-normal (+)' },
      ]}
    />
  );
}

function InvariantEditor({ terms, setTerms, tol, setTol, ledgerOptions, defaultLedger }: {
  terms: Term[]; setTerms: (t: Term[]) => void; tol: Amount[]; setTol: (a: Amount[]) => void;
  ledgerOptions: string[]; defaultLedger: string;
}) {
  const update = (i: number, patch: Partial<Term>) => setTerms(terms.map((t, idx) => (idx === i ? { ...t, ...patch } : t)));
  return (
    <>
      <div className="space-y-3">
        <div className="space-y-1">
          <Label className="text-sm">Account sets to balance</Label>
          <p className="text-xs leading-relaxed text-muted-foreground">
            Choose each account set’s normal balance. Debit-normal balances contribute a negative amount; credit-normal balances contribute a positive amount. The normalized total must equal zero.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border bg-muted/30 px-3 py-2 text-xs">
          <span className="font-medium text-foreground">Example</span>
          <span className="text-muted-foreground">Debit-normal 100 → −100</span>
          <span className="text-muted-foreground">+</span>
          <span className="text-muted-foreground">Credit-normal 100 → +100</span>
          <span className="font-mono font-medium text-foreground">= 0</span>
        </div>
        {terms.map((t, i) => (
          <div key={i} className="space-y-3 rounded-md border bg-background p-3">
            <div className="flex items-center justify-between gap-3">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Account set {i + 1}</span>
              <Button type="button" size="icon-sm" variant="ghost" onClick={() => setTerms(terms.filter((_, idx) => idx !== i))} disabled={terms.length === 1} aria-label="Remove term">
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
            <div className="grid items-start gap-3 md:grid-cols-[19rem_10rem_minmax(0,1fr)]">
              <Field label="Normal balance">
                <NormalBalanceControl sign={t.sign} onChange={(sign) => update(i, { sign })} ariaLabel={`Account set ${i + 1} normal balance`} />
              </Field>
              <Field label="Ledger">
                <LedgerCombobox value={t.ledger} onChange={(ledger) => update(i, { ledger })} options={ledgerOptions} placeholder="treasury" />
              </Field>
              <Field label="Account selector">
                <AccountSelectorInput ledger={t.ledger} value={t.address} onChange={(address) => update(i, { address })} placeholder="assets:*" />
              </Field>
            </div>
            <p className="text-xs text-muted-foreground">
              {t.sign < 0
                ? 'Debit-normal: a balance of 100 is normalized to −100.'
                : 'Credit-normal: a balance of 100 is normalized to +100.'}
            </p>
            <MetaBlock ledger={t.ledger} query={t} setQuery={(q) => update(i, q)} />
          </div>
        ))}
        <Button type="button" size="sm" variant="outline" onClick={() => setTerms([...terms, { ledger: terms[0]?.ledger || defaultLedger, sign: terms.length % 2 === 0 ? -1 : 1, ...emptyQuery() }])}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Add account set
        </Button>
      </div>
      <div className="space-y-1.5">
        <Label className="text-sm">Tolerance per asset <span className="font-normal text-muted-foreground">— {MINOR_UNITS_HINT}</span></Label>
        <AmountRows rows={tol} setRows={setTol} addLabel="Add asset" />
      </div>
    </>
  );
}

// ── source_parity ─────────────────────────────────────────────────────────
function SourceFields({ label, src, setSrc, ledgerOptions }: { label: string; src: Source; setSrc: (s: Source) => void; ledgerOptions: string[] }) {
  const isMeta = src.kind === 'account_metadata';
  const fieldPrefix = label === 'Source A' ? 'source-a' : 'source-b';
  return (
    <div className="space-y-3 rounded-lg border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{label}</span>
        <Segmented
          ariaLabel={`${label} source kind`}
          value={src.kind}
          onChange={(v) => setSrc({ ...src, kind: v })}
          options={PARITY_SOURCE_KINDS}
        />
      </div>

      <div className="grid gap-3 md:grid-cols-[12rem_1fr]">
        <Field label="Ledger to read">
          <LedgerCombobox value={src.ledger} onChange={(ledger) => setSrc({ ...src, ledger })} options={ledgerOptions} placeholder="book" />
        </Field>
        <Field label="Account selector" htmlFor={`${fieldPrefix}-account-selector`}>
          <AccountSelectorInput id={`${fieldPrefix}-account-selector`} ledger={src.ledger} value={src.address} onChange={(address) => setSrc({ ...src, address })} placeholder={isMeta ? 'mirror:custody (address selector)' : 'cash:* (address selector)'} />
        </Field>
      </div>

      {isMeta && (
        <div className="space-y-3 rounded-md border bg-muted/30 p-2.5">
          <p className="text-[11px] text-muted-foreground">Read one integer from account metadata and treat it as the balance for one declared asset. Postings are not read on this side.</p>
          <Field label="Metadata key" htmlFor={`${fieldPrefix}-metadata-key`}>
            <Input id={`${fieldPrefix}-metadata-key`} className="font-mono text-sm" value={src.metadataKey} onChange={(e) => setSrc({ ...src, metadataKey: e.target.value })} placeholder="reported.USD or value_known.toto" />
            <p className="text-[11px] text-muted-foreground">
              The key is opaque and does not need to contain the asset code.
            </p>
          </Field>
          <Field label="Asset represented" htmlFor={`${fieldPrefix}-asset`}>
            <Input id={`${fieldPrefix}-asset`} className="font-mono text-sm" value={src.asset} onChange={(e) => setSrc({ ...src, asset: e.target.value })} placeholder="USD/2" />
            <p className="text-[11px] text-muted-foreground">
              The stored value must be an integer in this asset’s minor units. Configure another rule for each additional metadata-key/asset pair.
            </p>
          </Field>
        </div>
      )}

      <MetaBlock ledger={src.ledger} query={src} setQuery={(q) => setSrc({ ...src, ...q })} collapsible />
    </div>
  );
}

function ParityEditor({ left, setLeft, right, setRight, scope, setScope, tol, setTol, ledgerOptions }: {
  left: Source; setLeft: (s: Source) => void; right: Source; setRight: (s: Source) => void;
  scope: 'aggregate' | 'per_account'; setScope: (s: 'aggregate' | 'per_account') => void;
  tol: Amount[]; setTol: (a: Amount[]) => void; ledgerOptions: string[];
}) {
  // A synced-metadata side is aggregate-only. Reflect that in the control (show
  // 'aggregate', disable per_account); buildSpec coerces the emitted scope too.
  const hasMeta = left.kind === 'account_metadata' || right.kind === 'account_metadata';
  const effScope = hasMeta ? 'aggregate' : scope;
  const sideSpec = (src: Source) => buildParitySide({
    kind: src.kind,
    ledger: src.ledger.trim(),
    query: compileReconQuery(src.address, src.meta, src.metaComb),
    metadataKey: src.metadataKey,
    asset: src.asset,
  });
  const metadataAsset = left.kind === 'account_metadata' ? left.asset.trim() : right.kind === 'account_metadata' ? right.asset.trim() : '';
  const metadataTolerance = tol.find((row) => row.asset.trim() === metadataAsset)?.amount ?? '0';
  const tolerance = metadataAsset
    ? { [metadataAsset]: Number(metadataTolerance || '0') }
    : Object.fromEntries(tol.filter((row) => row.asset.trim()).map((row) => [row.asset.trim(), Number(row.amount || '0')]));
  const previewSpec = { left: sideSpec(left), right: sideSpec(right), scope: effScope, tolerance };
  return (
    <>
      <p className="text-xs text-muted-foreground">
        {hasMeta
          ? 'Choose where each value comes from. This rule checks the declared metadata asset and opens a break when the difference exceeds its tolerance.'
          : 'Choose where each value comes from. The rule compares both sources per asset and opens a break when the difference exceeds the tolerance.'}
      </p>

      <div className="space-y-2">
        <SourceFields label="Source A" src={left} setSrc={setLeft} ledgerOptions={ledgerOptions} />
        <div className="flex items-center gap-3 px-1">
          <div className="h-px flex-1 bg-border" />
          <span className="rounded-full border bg-muted/50 px-2 py-0.5 text-[11px] font-medium text-muted-foreground">must equal</span>
          <div className="h-px flex-1 bg-border" />
        </div>
        <SourceFields label="Source B" src={right} setSrc={setRight} ledgerOptions={ledgerOptions} />
      </div>

      <div className="space-y-2 rounded-lg border bg-muted/15 p-3">
        <p className="text-xs font-semibold">Rule preview</p>
        <SourceParityComparison spec={previewSpec} density="compact" preview />
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Label className="text-sm">Scope</Label>
        <Segmented
          ariaLabel="Scope"
          value={effScope}
          onChange={setScope}
          options={SCOPE_OPTIONS}
          disabledValues={hasMeta ? ['per_account'] : []}
        />
        {hasMeta && <span className="text-[11px] text-muted-foreground">Per-account isn’t available with a synced-metadata source.</span>}
      </div>

      <div className="space-y-1.5">
        <Label className="text-sm">{hasMeta ? 'Tolerance for declared asset' : 'Tolerance per asset'} <span className="font-normal text-muted-foreground">— optional; {MINOR_UNITS_HINT}</span></Label>
        {hasMeta ? (
          <div className="flex flex-wrap items-center gap-2">
            <Input className="w-28 font-mono" value={metadataAsset} readOnly aria-label="Tolerance asset" placeholder="USD/2" />
            <Input
              className="min-w-36 flex-1"
              inputMode="numeric"
              value={metadataTolerance}
              onChange={(event) => setTol([{ asset: metadataAsset, amount: event.target.value }])}
              placeholder="0"
              aria-label="Tolerance amount"
            />
          </div>
        ) : (
          <AmountRows rows={tol} setRows={setTol} addLabel="Add asset" />
        )}
      </div>
    </>
  );
}

// ── account_threshold ───────────────────────────────────────────────────────
function ThresholdEditor({ ledger, setLedger, query, setQuery, mode, setMode, bounds, setBounds, ledgerOptions }: {
  ledger: string; setLedger: (s: string) => void; query: Query; setQuery: (q: Query) => void;
  mode: 'aggregate' | 'per_account'; setMode: (m: 'aggregate' | 'per_account') => void;
  bounds: Bound[]; setBounds: (b: Bound[]) => void; ledgerOptions: string[];
}) {
  const update = (i: number, patch: Partial<Bound>) => setBounds(bounds.map((b, idx) => (idx === i ? { ...b, ...patch } : b)));
  return (
    <>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Ledger"><LedgerCombobox value={ledger} onChange={setLedger} options={ledgerOptions} placeholder="treasury" /></Field>
        <Field label="Mode">
          <Select value={mode} onValueChange={(v) => setMode(v as 'aggregate' | 'per_account')}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="aggregate">Aggregate (sum the set)</SelectItem>
              <SelectItem value="per_account">Per account (one alert each)</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>
      <Field label="Accounts"><AccountSelectorInput ledger={ledger} value={query.address} onChange={(address) => setQuery({ ...query, address })} placeholder="reserves:usd:* (address selector)" /></Field>
      <MetaBlock ledger={ledger} query={query} setQuery={setQuery} />
      <div className="space-y-2">
        <Label className="text-sm">Bounds per asset <span className="font-normal text-muted-foreground">— at least one of min/max; {MINOR_UNITS_HINT}</span></Label>
        {bounds.map((b, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <Input className="w-24" value={b.asset} onChange={(e) => update(i, { asset: e.target.value })} placeholder="USD" />
            <Input className="min-w-28 flex-1" inputMode="numeric" value={b.min} onChange={(e) => update(i, { min: e.target.value })} placeholder="min" />
            <Input className="min-w-28 flex-1" inputMode="numeric" value={b.max} onChange={(e) => update(i, { max: e.target.value })} placeholder="max" />
            <Button type="button" size="icon-sm" variant="ghost" onClick={() => setBounds(bounds.filter((_, idx) => idx !== i))} disabled={bounds.length === 1} aria-label="Remove bound">
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        ))}
        <Button type="button" size="sm" variant="outline" onClick={() => setBounds([...bounds, { asset: '', min: '', max: '' }])}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Add asset
        </Button>
      </div>
    </>
  );
}
