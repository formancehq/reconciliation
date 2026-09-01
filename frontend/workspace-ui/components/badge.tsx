import { cva, type VariantProps } from 'class-variance-authority';
import * as React from 'react';

import { cn } from '@workspace/ui/lib/utils';
const badgeVariants = cva(
  'items-center flex whitespace-nowrap w-fit border border-transparent rounded-full font-medium text-[var(--tag-foreground)] transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 font-medium font-mono uppercase tracking-wide',
  {
    variants: {
      variant: {
        primary: 'bg-primary text-primary-foreground',
        secondary: 'bg-secondary text-secondary-foreground',
        outline: 'border-border text-foreground',
        emerald: 'bg-emerald-600 text-emerald-200',
        slate: 'bg-emerald-300 text-emerald-800',
        lilac: 'bg-lilac-400 text-lilac-800',
        gold: 'bg-gold-500 text-emerald-100',
        cobalt: 'bg-cobalt-500 text-emerald-100',
        cobaltDark: 'bg-cobalt-700 text-emerald-100',
        mint: 'bg-mint-500 text-mint-900',
        valid: 'bg-valid text-valid-foreground',
        destructive: 'bg-destructive text-destructive-foreground',
        info: 'bg-info text-info-foreground',
        warning: 'bg-warning text-warning-foreground',

        red: 'bg-red-background text-red-foreground',
        orange: 'bg-orange-background text-orange-foreground',
        amber: 'bg-amber-background text-amber-foreground',
        yellow: 'bg-yellow-background text-yellow-foreground',
        lime: 'bg-lime-background text-lime-foreground',
        green: 'bg-green-background text-green-foreground',
        teal: 'bg-teal-background text-teal-foreground',
        cyan: 'bg-cyan-background text-cyan-foreground',
        sky: 'bg-sky-background text-sky-foreground',
        blue: 'bg-blue-background text-blue-foreground',
        indigo: 'bg-indigo-background text-indigo-foreground',
        violet: 'bg-violet-background text-violet-foreground',
        purple: 'bg-purple-background text-purple-foreground',
        fuchsia: 'bg-fuchsia-background text-fuchsia-foreground',
        pink: 'bg-pink-background text-pink-foreground',
        rose: 'bg-rose-background text-rose-foreground',
        zinc: 'bg-zinc-background text-zinc-foreground',
      },
      size: {
        sm: 'h-5 px-1.5 gap-1 text-[10px]/none',
        md: 'h-6 px-2.5 gap-2 text-xs/none',
        lg: 'h-7 px-3 gap-2 text-sm/none',
      },
      isDisabled: {
        true: 'cursor-not-allowed opacity-50',
        false: '',
      },
    },
    defaultVariants: {
      variant: 'primary',
      size: 'md',
    },
  }
);

export type TBadgeProps = React.HTMLAttributes<HTMLDivElement> &
  VariantProps<typeof badgeVariants>;

function Badge({
  className,
  variant,
  size,
  isDisabled,
  ...props
}: TBadgeProps) {
  return (
    <div
      data-slot="badge"
      className={cn(badgeVariants({ variant, size, isDisabled }), className)}
      {...props}
    />
  );
}

export { Badge, badgeVariants };
