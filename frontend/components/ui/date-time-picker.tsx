'use client';

/**
 * DateTimePicker Component
 * A popover-based date and time picker with quick presets
 */

import * as React from 'react';
import { Calendar as CalendarIcon, Clock, ChevronLeft, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';

interface DateTimePickerProps {
  value?: string; // ISO string format YYYY-MM-DDTHH:MM or empty
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  /** Selectable lower/upper bounds (ISO); days outside are greyed out. */
  min?: string;
  max?: string;
  /** Hide the built-in quick single-date presets (e.g. when a range picker owns presets). */
  hidePresets?: boolean;
}

// Helper functions (no date-fns dependency)
const formatDate = (date: Date): string => {
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return `${months[date.getMonth()]} ${date.getDate()}, ${date.getFullYear()}`;
};

const formatTime = (date: Date): string => {
  return `${date.getHours().toString().padStart(2, '0')}:${date.getMinutes().toString().padStart(2, '0')}`;
};

const subHours = (date: Date, hours: number): Date => new Date(date.getTime() - hours * 60 * 60 * 1000);
const subDays = (date: Date, days: number): Date => new Date(date.getTime() - days * 24 * 60 * 60 * 1000);

const startOfDay = (date: Date): Date => {
  const d = new Date(date);
  d.setHours(0, 0, 0, 0);
  return d;
};

const startOfWeek = (date: Date): Date => {
  const d = new Date(date);
  const day = d.getDay();
  d.setDate(d.getDate() - day);
  d.setHours(0, 0, 0, 0);
  return d;
};

const startOfMonth = (date: Date): Date => {
  const d = new Date(date);
  d.setDate(1);
  d.setHours(0, 0, 0, 0);
  return d;
};

// Quick presets for common time ranges
const presets = [
  { label: 'Now', getValue: () => new Date() },
  { label: '1h ago', getValue: () => subHours(new Date(), 1) },
  { label: '24h ago', getValue: () => subHours(new Date(), 24) },
  { label: '7d ago', getValue: () => subDays(new Date(), 7) },
  { label: '30d ago', getValue: () => subDays(new Date(), 30) },
  { label: 'Start of today', getValue: () => startOfDay(new Date()) },
  { label: 'Start of week', getValue: () => startOfWeek(new Date()) },
  { label: 'Start of month', getValue: () => startOfMonth(new Date()) },
];

export function DateTimePicker({ value, onChange, placeholder = 'Select date & time', className, min, max, hidePresets }: DateTimePickerProps) {
  const [open, setOpen] = React.useState(false);
  const minD = min ? new Date(min) : null;
  const maxD = max ? new Date(max) : null;
  const dayOutOfRange = (y: number, m: number, d: number): boolean => {
    const ds = startOfDay(new Date(y, m, d));
    if (minD && ds < startOfDay(minD)) return true;
    if (maxD && ds > startOfDay(maxD)) return true;
    return false;
  };
  const dateOutOfRange = (d: Date): boolean => (!!minD && d < minD) || (!!maxD && d > maxD);
  const [internalDate, setInternalDate] = React.useState<Date | null>(() => {
    if (value && value.includes('T')) {
      return new Date(value);
    }
    return null;
  });

  // Sync internal state with prop
  React.useEffect(() => {
    if (value && value.includes('T')) {
      setInternalDate(new Date(value));
    } else if (!value) {
      setInternalDate(null);
    }
  }, [value]);

  // Parse current date components
  const now = new Date();
  const year = internalDate?.getFullYear() || now.getFullYear();
  const month = internalDate?.getMonth() || now.getMonth();
  const day = internalDate?.getDate() || now.getDate();
  const hours = internalDate?.getHours() || 0;
  const minutes = internalDate?.getMinutes() || 0;

  const updateDateTime = (updates: { year?: number; month?: number; day?: number; hours?: number; minutes?: number }) => {
    const newDate = new Date(
      updates.year ?? year,
      updates.month ?? month,
      updates.day ?? day,
      updates.hours ?? hours,
      updates.minutes ?? minutes
    );
    setInternalDate(newDate);
    // Format: YYYY-MM-DDTHH:MM (for datetime-local compatibility)
    const isoStr = `${newDate.getFullYear()}-${(newDate.getMonth() + 1).toString().padStart(2, '0')}-${newDate.getDate().toString().padStart(2, '0')}T${newDate.getHours().toString().padStart(2, '0')}:${newDate.getMinutes().toString().padStart(2, '0')}`;
    onChange(isoStr);
  };

  const applyPreset = (preset: typeof presets[0]) => {
    const date = preset.getValue();
    setInternalDate(date);
    const isoStr = `${date.getFullYear()}-${(date.getMonth() + 1).toString().padStart(2, '0')}-${date.getDate().toString().padStart(2, '0')}T${date.getHours().toString().padStart(2, '0')}:${date.getMinutes().toString().padStart(2, '0')}`;
    onChange(isoStr);
    setOpen(false);
  };

  const clearDate = () => {
    setInternalDate(null);
    onChange('');
    setOpen(false);
  };

  // Calendar view state
  const [viewMonth, setViewMonth] = React.useState(month);
  const [viewYear, setViewYear] = React.useState(year);

  React.useEffect(() => {
    if (internalDate) {
      setViewMonth(internalDate.getMonth());
      setViewYear(internalDate.getFullYear());
    }
  }, [internalDate]);

  // Reset view when opening
  React.useEffect(() => {
    if (open) {
      setViewMonth(internalDate?.getMonth() || now.getMonth());
      setViewYear(internalDate?.getFullYear() || now.getFullYear());
    }
  }, [open]);

  const daysInMonth = new Date(viewYear, viewMonth + 1, 0).getDate();
  const firstDayOfMonth = new Date(viewYear, viewMonth, 1).getDay();
  const days = Array.from({ length: daysInMonth }, (_, i) => i + 1);
  const emptyDays = Array.from({ length: firstDayOfMonth }, (_, i) => i);

  const prevMonth = () => {
    if (viewMonth === 0) {
      setViewMonth(11);
      setViewYear(viewYear - 1);
    } else {
      setViewMonth(viewMonth - 1);
    }
  };

  const nextMonth = () => {
    if (viewMonth === 11) {
      setViewMonth(0);
      setViewYear(viewYear + 1);
    } else {
      setViewMonth(viewMonth + 1);
    }
  };

  const prevMonthDisabled = minD ? new Date(viewYear, viewMonth, 0) < startOfDay(minD) : false;
  const nextMonthDisabled = maxD ? new Date(viewYear, viewMonth + 1, 1) > startOfDay(maxD) : false;

  const monthNames = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  const dayNames = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa'];

  const isSelectedDay = (d: number) => 
    internalDate && 
    d === internalDate.getDate() && 
    viewMonth === internalDate.getMonth() && 
    viewYear === internalDate.getFullYear();

  const isToday = (d: number) => {
    const today = new Date();
    return d === today.getDate() && viewMonth === today.getMonth() && viewYear === today.getFullYear();
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          className={cn(
            'justify-start text-left font-normal h-8',
            !value && 'text-muted-foreground',
            className
          )}
        >
          <CalendarIcon className="mr-2 h-3.5 w-3.5" />
          {internalDate ? (
            <span className="text-xs">
              {formatDate(internalDate)} <span className="text-muted-foreground">at</span> {formatTime(internalDate)}
            </span>
          ) : (
            <span className="text-xs">{placeholder}</span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <div className="flex">
          {/* Calendar Section */}
          <div className="p-3 border-r">
            {/* Month/Year Navigation */}
            <div className="flex items-center justify-between mb-2">
              <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={prevMonth} disabled={prevMonthDisabled}>
                <ChevronLeft className="h-4 w-4" />
              </Button>
              <span className="text-sm font-medium">
                {monthNames[viewMonth]} {viewYear}
              </span>
              <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={nextMonth} disabled={nextMonthDisabled}>
                <ChevronRight className="h-4 w-4" />
              </Button>
            </div>

            {/* Day Names */}
            <div className="grid grid-cols-7 gap-1 mb-1">
              {dayNames.map(d => (
                <div key={d} className="text-center text-[10px] text-muted-foreground font-medium w-7">
                  {d}
                </div>
              ))}
            </div>

            {/* Calendar Days */}
            <div className="grid grid-cols-7 gap-1">
              {emptyDays.map(i => (
                <div key={`empty-${i}`} className="w-7 h-7" />
              ))}
              {days.map(d => {
                const dis = dayOutOfRange(viewYear, viewMonth, d);
                return (
                <Button
                  key={d}
                  variant={isSelectedDay(d) ? 'default' : 'ghost'}
                  size="sm"
                  disabled={dis}
                  className={cn(
                    'h-7 w-7 p-0 text-xs',
                    isToday(d) && !isSelectedDay(d) && 'bg-accent text-accent-foreground',
                    isSelectedDay(d) && 'bg-primary text-primary-foreground',
                    dis && 'opacity-30 cursor-not-allowed'
                  )}
                  onClick={() => updateDateTime({ year: viewYear, month: viewMonth, day: d })}
                >
                  {d}
                </Button>
                );
              })}
            </div>
          </div>

          {/* Time & Presets Section */}
          <div className="p-3 w-[140px]">
            {/* Time Input */}
            <div className="mb-3">
              <Label className="text-xs text-muted-foreground mb-1.5 flex items-center gap-1">
                <Clock className="h-3 w-3" /> Time
              </Label>
              <div className="flex items-center gap-1">
                <Input
                  type="number"
                  min={0}
                  max={23}
                  value={hours.toString().padStart(2, '0')}
                  onChange={(e) => updateDateTime({ hours: Math.min(23, Math.max(0, parseInt(e.target.value) || 0)) })}
                  className="w-12 h-8 text-center text-sm px-1"
                />
                <span className="text-muted-foreground">:</span>
                <Input
                  type="number"
                  min={0}
                  max={59}
                  value={minutes.toString().padStart(2, '0')}
                  onChange={(e) => updateDateTime({ minutes: Math.min(59, Math.max(0, parseInt(e.target.value) || 0)) })}
                  className="w-12 h-8 text-center text-sm px-1"
                />
              </div>
            </div>

            {/* Quick Presets */}
            {!hidePresets && (
              <div>
                <Label className="text-xs text-muted-foreground mb-1.5 block">Quick</Label>
                <div className="space-y-1 max-h-[180px] overflow-y-auto">
                  {presets.map(preset => {
                    const dis = dateOutOfRange(preset.getValue());
                    return (
                      <Button
                        key={preset.label}
                        variant="ghost"
                        size="sm"
                        disabled={dis}
                        className={cn('w-full h-6 justify-start text-xs px-2', dis && 'opacity-30 cursor-not-allowed')}
                        onClick={() => applyPreset(preset)}
                      >
                        {preset.label}
                      </Button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between p-2 border-t bg-muted/30">
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={clearDate}>
              Clear
            </Button>
            <span className="text-[10px] text-muted-foreground">
              {Intl.DateTimeFormat().resolvedOptions().timeZone}
            </span>
          </div>
          <Button size="sm" className="h-7 text-xs" onClick={() => setOpen(false)}>
            Done
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
