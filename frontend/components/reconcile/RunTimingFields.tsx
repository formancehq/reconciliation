"use client";

/**
 * The shared "when should it evaluate?" form section: alert grouping period and
 * run mode. It lived in the V1 rule dialog, which went with the V1 template
 * catalogue; this is the part the rule dialog still uses.
 */
import {
  CalendarClock,
  Check,
  ChevronRight,
  MousePointerClick,
} from 'lucide-react';
import { cn } from '@workspace/ui/lib/utils';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  RadioGroup,
  RadioGroupItem,
} from '@workspace/ui/components/radio-group';
import {
  PERIOD_TYPE_META,
  type PeriodType,
  type Schedule,
} from '@/lib/recon';

const PERIOD_TYPES: PeriodType[] = ['continuous', 'daily', 'weekly', 'monthly'];
const PERIOD_TYPE_DESCRIPTIONS: Record<PeriodType, string> = {
  continuous: 'One ongoing alert thread',
  daily: 'One alert thread per day',
  weekly: 'One alert thread per week',
  monthly: 'One alert thread per month',
};

const ALERT_PERIOD_COPY: Record<PeriodType, {
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

const DEFAULT_CRON: Record<PeriodType, string> = {
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
function scheduleDescription(schedule: Schedule): string {
  if (schedule.kind === 'on_demand') return 'Runs only when someone chooses Run now.';
  const preset = CRON_PRESETS.find(({ expr }) => expr === schedule.expr);
  return preset
    ? `Runs automatically: ${preset.label.toLowerCase()} (${schedule.tz?.trim() || 'UTC'}).`
    : `Runs automatically on the custom cron schedule (${schedule.tz?.trim() || 'UTC'}).`;
}

export function RunTimingFields({ periodType, schedule, periodTypeLocked = false, onPeriodTypeChange, onScheduleChange }: {
  periodType: PeriodType;
  schedule: Schedule;
  periodTypeLocked?: boolean;
  onPeriodTypeChange: (value: PeriodType) => void;
  onScheduleChange: (value: Schedule) => void;
}) {
  const periodCopy = ALERT_PERIOD_COPY[periodType];
  return (
    <fieldset className="space-y-4 rounded-md border bg-muted/15 p-4">
      <legend className="text-sm font-semibold">When should it evaluate?</legend>
      <p className="-mt-3 text-xs leading-relaxed text-muted-foreground">
        Alert period groups related failing observations into alert threads. Run mode separately controls when evaluations happen.
      </p>

      <div className="space-y-2">
        <div className="flex items-baseline justify-between gap-3">
          <Label className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Alert grouping period</Label>
          {periodTypeLocked && <span className="text-xs text-muted-foreground">Fixed after creation</span>}
        </div>
        <RadioGroup
          value={periodType}
          onValueChange={(next) => onPeriodTypeChange(next as PeriodType)}
          disabled={periodTypeLocked}
          className="grid gap-2 sm:grid-cols-2 md:grid-cols-4"
          aria-label="Alert grouping period"
        >
          {PERIOD_TYPES.map((option) => (
            <label
              key={option}
              className={cn(
                'flex min-h-16 cursor-pointer items-start gap-2.5 rounded-md border bg-background p-3 transition-colors',
                'hover:bg-muted/30 focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/30',
                periodType === option && 'border-primary bg-primary/5 ring-1 ring-primary/20',
                periodTypeLocked && 'cursor-not-allowed opacity-60',
              )}
            >
              <RadioGroupItem value={option} className="mt-0.5" aria-label={PERIOD_TYPE_META[option].label} />
              <span>
                <span className="block text-sm font-medium">{PERIOD_TYPE_META[option].label}</span>
                <span className="mt-0.5 block text-xs leading-snug text-muted-foreground">{PERIOD_TYPE_DESCRIPTIONS[option]}</span>
              </span>
            </label>
          ))}
        </RadioGroup>
      </div>

      <div className="space-y-2 rounded-md border bg-background p-3" role="note" aria-label={`${PERIOD_TYPE_META[periodType].label} alert lifecycle`}>
        <div>
          <p className="text-xs font-semibold text-foreground">How the {PERIOD_TYPE_META[periodType].label.toLowerCase()} period works</p>
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
              ? { kind: 'cron', expr: schedule.expr || DEFAULT_CRON[periodType], tz: schedule.tz }
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
                placeholder={DEFAULT_CRON[periodType]}
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
        <span><strong className="font-medium text-foreground">{PERIOD_TYPE_META[periodType].label} alert period.</strong> {PERIOD_TYPE_DESCRIPTIONS[periodType]}. {scheduleDescription(schedule)}</span>
      </p>
    </fieldset>
  );
}

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