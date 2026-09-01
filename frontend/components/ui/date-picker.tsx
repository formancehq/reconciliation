'use client';

/**
 * DatePicker Component
 * A popover-based date picker (no time) with quick presets
 */

import * as React from 'react';
import { Calendar as CalendarIcon, ChevronLeft, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';

interface DatePickerProps {
  value?: string; // ISO string format YYYY-MM-DD or empty
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
}

// Helper functions
const formatDate = (date: Date): string => {
  const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
  return `${months[date.getMonth()]} ${date.getDate()}, ${date.getFullYear()}`;
};

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

const endOfMonth = (date: Date): Date => {
  const d = new Date(date.getFullYear(), date.getMonth() + 1, 0);
  d.setHours(0, 0, 0, 0);
  return d;
};

const toISODate = (date: Date): string => {
  return `${date.getFullYear()}-${(date.getMonth() + 1).toString().padStart(2, '0')}-${date.getDate().toString().padStart(2, '0')}`;
};

// Quick presets for dates
const presets = [
  { label: 'Today', getValue: () => startOfDay(new Date()) },
  { label: 'Yesterday', getValue: () => subDays(startOfDay(new Date()), 1) },
  { label: '7 days ago', getValue: () => subDays(startOfDay(new Date()), 7) },
  { label: '30 days ago', getValue: () => subDays(startOfDay(new Date()), 30) },
  { label: 'Start of week', getValue: () => startOfWeek(new Date()) },
  { label: 'Start of month', getValue: () => startOfMonth(new Date()) },
  { label: 'End of month', getValue: () => endOfMonth(new Date()) },
];

export function DatePicker({ value, onChange, placeholder = 'Select date', className }: DatePickerProps) {
  const [open, setOpen] = React.useState(false);
  const [internalDate, setInternalDate] = React.useState<Date | null>(() => {
    if (value) {
      const d = new Date(value + 'T00:00:00');
      return isNaN(d.getTime()) ? null : d;
    }
    return null;
  });

  // Sync internal state with prop
  React.useEffect(() => {
    if (value) {
      const d = new Date(value + 'T00:00:00');
      if (!isNaN(d.getTime())) {
        setInternalDate(d);
      }
    } else {
      setInternalDate(null);
    }
  }, [value]);

  const now = new Date();
  const year = internalDate?.getFullYear() || now.getFullYear();
  const month = internalDate?.getMonth() || now.getMonth();

  const selectDate = (newYear: number, newMonth: number, newDay: number) => {
    const newDate = new Date(newYear, newMonth, newDay);
    setInternalDate(newDate);
    onChange(toISODate(newDate));
  };

  const applyPreset = (preset: typeof presets[0]) => {
    const date = preset.getValue();
    setInternalDate(date);
    onChange(toISODate(date));
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
            <span className="text-xs">{formatDate(internalDate)}</span>
          ) : (
            <span className="text-xs">{placeholder}</span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-auto p-0" align="start">
        <div className="flex">
          {/* Calendar Section */}
          <div className="p-3">
            {/* Month/Year Navigation */}
            <div className="flex items-center justify-between mb-2">
              <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={prevMonth}>
                <ChevronLeft className="h-4 w-4" />
              </Button>
              <span className="text-sm font-medium">
                {monthNames[viewMonth]} {viewYear}
              </span>
              <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={nextMonth}>
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
              {days.map(d => (
                <Button
                  key={d}
                  variant={isSelectedDay(d) ? 'default' : 'ghost'}
                  size="sm"
                  className={cn(
                    'h-7 w-7 p-0 text-xs',
                    isToday(d) && !isSelectedDay(d) && 'bg-accent text-accent-foreground',
                    isSelectedDay(d) && 'bg-primary text-primary-foreground'
                  )}
                  onClick={() => {
                    selectDate(viewYear, viewMonth, d);
                    setOpen(false);
                  }}
                >
                  {d}
                </Button>
              ))}
            </div>
          </div>

          {/* Presets Section */}
          <div className="p-3 border-l w-[120px]">
            <Label className="text-xs text-muted-foreground mb-1.5 block">Quick</Label>
            <div className="space-y-1">
              {presets.map(preset => (
                <Button
                  key={preset.label}
                  variant="ghost"
                  size="sm"
                  className="w-full h-6 justify-start text-xs px-2"
                  onClick={() => applyPreset(preset)}
                >
                  {preset.label}
                </Button>
              ))}
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between p-2 border-t bg-muted/30">
          <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={clearDate}>
            Clear
          </Button>
          <Button size="sm" className="h-7 text-xs" onClick={() => setOpen(false)}>
            Done
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
