'use client';

/**
 * DateRangePicker
 * A single hotel-booking-style range picker: one popover with the Formance DS
 * range calendar (click start then end), built-in Start/End time inputs, and a
 * presets column on the right. Values are exchanged as local `YYYY-MM-DDTHH:MM`
 * strings so they round-trip through the explorer's filter rules.
 */

import * as React from 'react';
import { Calendar as CalendarIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Calendar } from '@/components/ui/calendar';

export interface DateRangePreset {
  label: string;
  from: string;
  to: string;
}

interface DateRangePickerProps {
  from?: string;
  to?: string;
  onChange: (from: string, to: string) => void;
  /** Selectable lower/upper bounds (ISO); days outside are disabled. */
  min?: string;
  max?: string;
  presets?: DateRangePreset[];
  placeholder?: string;
  className?: string;
}

const pad = (n: number) => n.toString().padStart(2, '0');
const toStr = (d?: Date): string =>
  d ? `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}` : '';
const parse = (s?: string): Date | undefined => {
  if (!s) return undefined;
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? undefined : d;
};
const fmtLabel = (d: Date): string => d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });

export function DateRangePicker({ from, to, onChange, min, max, presets = [], placeholder = 'Select range', className }: DateRangePickerProps) {
  const [open, setOpen] = React.useState(false);

  const range = React.useMemo(() => {
    const f = parse(from);
    const t = parse(to);
    return f || t ? { from: f, to: t } : undefined;
  }, [from, to]);

  const minD = parse(min);
  const maxD = parse(max);
  const disabled = [
    ...(minD ? [{ before: minD }] : []),
    ...(maxD ? [{ after: maxD }] : []),
  ];

  const label = range?.from
    ? `${fmtLabel(range.from)}${range.to ? ` \u2013 ${fmtLabel(range.to)}` : ''}`
    : placeholder;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          className={cn('h-7 justify-start gap-2 text-left font-normal', !range?.from && 'text-muted-foreground', className)}
        >
          <CalendarIcon className="h-3.5 w-3.5 shrink-0" />
          <span className="truncate text-xs">{label}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <div className="flex">
          <Calendar
            mode="range"
            withTime
            numberOfMonths={2}
            selected={range}
            onSelect={(r) => onChange(toStr(r?.from), toStr(r?.to))}
            defaultMonth={range?.from ?? maxD}
            startMonth={minD}
            endMonth={maxD}
            disabled={disabled.length ? disabled : undefined}
          />
          {presets.length > 0 && (
            <div className="flex w-[160px] flex-col gap-1 border-l p-2">
              <span className="px-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Presets</span>
              <div className="flex flex-col gap-0.5 overflow-y-auto">
                {presets.map((p) => (
                  <Button
                    key={p.label}
                    variant="ghost"
                    size="sm"
                    className="h-7 justify-start text-xs font-normal"
                    onClick={() => onChange(p.from, p.to)}
                  >
                    {p.label}
                  </Button>
                ))}
              </div>
            </div>
          )}
        </div>
        <div className="flex items-center justify-between border-t bg-muted/30 p-2">
          <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => onChange('', '')}>Clear</Button>
          <Button size="sm" className="h-7 text-xs" onClick={() => setOpen(false)}>Done</Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
