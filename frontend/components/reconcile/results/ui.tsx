'use client';

/**
 * Small building blocks of the Results tab: exact amounts, enum chips, and the
 * bordered table every section uses (scrolls inside its box on a phone, never
 * the page).
 */
import type { ReactNode } from 'react';
import { Check, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { cn } from '@workspace/ui/lib/utils';
import {
  formatAmount,
  LETTERING_VERDICT_META,
  LIFECYCLE_META,
  OUTCOME_META,
  PRIORITY_META,
  words,
} from '@/lib/lettering/format';
import type { LetteringVerdict } from '@/lib/lettering/manifest';

export function Amount({
  value,
  asset,
  signed,
  className,
}: {
  value: bigint | string | number | null | undefined;
  asset: string;
  signed?: boolean;
  className?: string;
}) {
  return (
    <span className={cn('font-mono tabular-nums whitespace-nowrap', className)}>
      {formatAmount(value, asset, { signed })}
    </span>
  );
}

export function LetteringVerdictBadge({ verdict, size }: { verdict: string; size?: 'sm' | 'md' | 'lg' }) {
  const meta = LETTERING_VERDICT_META[verdict as LetteringVerdict] ?? { label: words(verdict), variant: 'outline' as const };
  return <Badge variant={meta.variant} size={size}>{meta.label}</Badge>;
}

export function PriorityBadge({ priority }: { priority: number }) {
  const meta = PRIORITY_META[priority] ?? { label: `P${priority}`, variant: 'outline' as const };
  return <Badge variant={meta.variant} size="sm">{meta.label}</Badge>;
}

export function LifecycleBadge({ lifecycle }: { lifecycle: string }) {
  const meta = LIFECYCLE_META[lifecycle] ?? { label: words(lifecycle), variant: 'outline' as const };
  return <Badge variant={meta.variant} size="sm">{meta.label}</Badge>;
}

export function OutcomeBadge({ outcome }: { outcome: string }) {
  const meta = OUTCOME_META[outcome] ?? { label: words(outcome), variant: 'outline' as const };
  return <Badge variant={meta.variant} size="sm">{meta.label}</Badge>;
}

/** An identity or continuity flag: ✓ when it holds. */
export function Holds({ ok, label }: { ok: boolean; label?: string }) {
  return ok ? (
    <Check className="inline h-3.5 w-3.5 text-green-foreground" aria-label={label ?? 'holds'} />
  ) : (
    <X className="inline h-3.5 w-3.5 text-destructive-foreground" aria-label={label ?? 'does not hold'} />
  );
}

export function SectionTitle({ title, hint, children }: { title: string; hint?: ReactNode; children?: ReactNode }) {
  return (
    <div className="mb-2 flex flex-wrap items-end justify-between gap-2">
      <div className="min-w-0">
        <h3 className="text-sm font-semibold">{title}</h3>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </div>
      {children}
    </div>
  );
}

/**
 * A bordered table that scrolls inside its box when the screen is narrower than
 * `minWidth`, or than its content with `fit`.
 */
export function DataTable({ minWidth = '40rem', fit, children }: { minWidth?: string; fit?: boolean; children: ReactNode }) {
  return (
    <div className="min-w-0 overflow-x-auto rounded-md border">
      <table className={cn('text-left text-xs', fit ? 'w-max min-w-full' : 'w-full')} style={fit ? undefined : { minWidth }}>
        {children}
      </table>
    </div>
  );
}

export function Th({ children, right, className }: { children?: ReactNode; right?: boolean; className?: string }) {
  return (
    <th className={cn('px-3 py-2 font-medium whitespace-nowrap', right && 'text-right', className)}>{children}</th>
  );
}

export function THead({ children }: { children: ReactNode }) {
  return (
    <thead className="bg-muted/35 text-[10px] tracking-wide text-muted-foreground uppercase">
      <tr>{children}</tr>
    </thead>
  );
}

export function Td({ children, right, className }: { children?: ReactNode; right?: boolean; className?: string }) {
  return <td className={cn('px-3 py-2 align-top', right && 'text-right', className)}>{children}</td>;
}
