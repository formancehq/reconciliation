'use client';

import { Check, ChevronDown } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';

/** Formance catalogue selector shared by Reconcile list filters and sorting. */
export function ReconFilterMenu({ value, onChange, allLabel, options, width, allValue = 'all' }: {
  value: string;
  onChange: (value: string) => void;
  allLabel: string;
  options: readonly (readonly [string, string])[];
  width: string;
  allValue?: string;
}) {
  const entries: readonly (readonly [string, string])[] = [[allValue, allLabel], ...options];
  const label = entries.find(([entryValue]) => entryValue === value)?.[1] ?? allLabel;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className={`h-8 ${width} min-w-0 justify-between bg-muted-lighter px-3 font-mono text-xs font-normal`}>
          <span className="truncate">{label}</span>
          <ChevronDown className="ml-2 h-3.5 w-3.5 shrink-0 opacity-50" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-(--radix-dropdown-menu-trigger-width)">
        {entries.map(([entryValue, optionLabel]) => (
          <DropdownMenuItem key={entryValue} onClick={() => onChange(entryValue)}>
            <span className="min-w-0 flex-1 truncate">{optionLabel}</span>
            {value === entryValue && <Check className="ml-auto h-4 w-4" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
