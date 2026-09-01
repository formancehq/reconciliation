'use client';

/**
 * A single editable combobox: one field that the user types into AND that
 * suggests matching values below. Unlike a button-triggered dropdown, the field
 * itself IS the search box (the typed text is both the value and the query), so
 * free-text entry and "pick from a list" are the same control. Suggestions are
 * fetched lazily while open and debounced on the input; picking one fills the
 * field, and any unmatched typed value is simply kept.
 */

import { useEffect, useRef, useState } from 'react';
import { Check, ChevronsUpDown, Loader2 } from 'lucide-react';

import { Input } from '@/components/ui/input';
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover';
import { cn } from '@/lib/utils';

export function ValueCombobox({
  value,
  onChange,
  placeholder,
  className,
  wrapperClassName,
  disabled,
  id,
  ariaLabel,
  showAllOnOpen = false,
  modal = false,
  fetchValues,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  wrapperClassName?: string;
  disabled?: boolean;
  id?: string;
  ariaLabel?: string;
  showAllOnOpen?: boolean;
  modal?: boolean;
  fetchValues: (search: string) => Promise<{ values: string[]; capped: boolean }>;
}) {
  const [open, setOpen] = useState(false);
  const [searchValue, setSearchValue] = useState('');
  const [values, setValues] = useState<string[]>([]);
  const [capped, setCapped] = useState(false);
  const [loading, setLoading] = useState(false);
  const reqId = useRef(0);
  const anchorRef = useRef<HTMLDivElement>(null);

  // Fetch while open, debounced on the field value (which doubles as the query),
  // so the server narrows as the user types. Stale responses are dropped.
  useEffect(() => {
    if (!open) return;
    const id = ++reqId.current;
    setLoading(true);
    const t = setTimeout(async () => {
      try {
        const res = await fetchValues(showAllOnOpen ? searchValue.trim() : value.trim());
        if (reqId.current !== id) return;
        setValues(res.values);
        setCapped(res.capped);
      } catch {
        if (reqId.current === id) { setValues([]); setCapped(false); }
      } finally {
        if (reqId.current === id) setLoading(false);
      }
    }, 250);
    return () => clearTimeout(t);
  }, [open, value, searchValue, showAllOnOpen, fetchValues]);

  const openWithInitialSearch = () => {
    if (!open && showAllOnOpen) setSearchValue('');
    setOpen(true);
  };

  const pick = (v: string) => { onChange(v); setOpen(false); };

  return (
    <Popover
      modal={modal}
      open={open && !disabled}
      onOpenChange={(nextOpen) => {
        if (nextOpen && showAllOnOpen) setSearchValue('');
        setOpen(nextOpen);
      }}
    >
      <PopoverAnchor asChild>
        <div ref={anchorRef} className={cn('relative inline-flex items-center', wrapperClassName)}>
          <Input
            id={id}
            aria-label={ariaLabel}
            value={value}
            disabled={disabled}
            onChange={(e) => {
              onChange(e.target.value);
              setSearchValue(e.target.value);
              setOpen(true);
            }}
            onFocus={openWithInitialSearch}
            onClick={openWithInitialSearch}
            onKeyDown={(e) => { if (e.key === 'Escape' || e.key === 'Enter') { if (e.key === 'Enter') e.preventDefault(); setOpen(false); } }}
            placeholder={placeholder}
            className={cn('pr-7', className)}
          />
          <span className="pointer-events-none absolute right-2 flex items-center text-muted-foreground">
            {loading ? <Loader2 className="h-3 w-3 animate-spin" /> : <ChevronsUpDown className="h-3 w-3 opacity-60" />}
          </span>
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={4}
        className="w-[max(11rem,var(--radix-popover-trigger-width))] p-1"
        onOpenAutoFocus={(e) => e.preventDefault()}
        onCloseAutoFocus={(e) => e.preventDefault()}
        onInteractOutside={(e) => { if (anchorRef.current?.contains(e.target as Node)) e.preventDefault(); }}
      >
        <div className="max-h-56 overscroll-contain overflow-y-auto">
          {loading && values.length === 0 && (
            <div className="flex items-center gap-2 px-2 py-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading…</div>
          )}
          {!loading && values.length === 0 && (
            <div className="px-2 py-2 text-xs text-muted-foreground">
              {value.trim() ? <>No matches — using <span className="font-mono">{value.trim()}</span></> : 'No values found.'}
            </div>
          )}
          {values.map((v) => (
            <button
              key={v}
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(v)}
              className="flex w-full items-center gap-2 rounded px-2 py-1 text-left font-mono text-xs hover:bg-accent"
            >
              <Check className={cn('h-3 w-3 shrink-0', value === v ? 'opacity-100' : 'opacity-0')} />
              <span className="truncate">{v}</span>
            </button>
          ))}
        </div>
        {capped && <div className="border-t px-2 py-1.5 text-[10px] text-muted-foreground">Showing first {values.length} — keep typing to narrow.</div>}
      </PopoverContent>
    </Popover>
  );
}
