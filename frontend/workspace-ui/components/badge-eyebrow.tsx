import { cn } from '@workspace/ui/lib/utils';
import { cva, type VariantProps } from 'class-variance-authority';
import { Slot as SlotPrimitive } from 'radix-ui';
import * as React from 'react';

const badgeEyebrowVariants = cva(
  'pointer-events-none inline-flex w-fit shrink-0 items-center justify-center gap-px rounded-[2px] border border-solid px-1.5 py-0.5 font-mono whitespace-nowrap uppercase',
  {
    variants: {
      variant: {
        primary: 'bg-primary border-primary text-primary-foreground',
        secondary: 'bg-secondary border-border text-secondary-foreground',
        outline: 'border-border text-foreground',
        emerald: 'bg-emerald-600 border-emerald-700 text-emerald-200',
        slate: 'bg-emerald-300 border-emerald-500 text-emerald-800',
        lilac: 'bg-lilac-400 border-lilac-600 text-lilac-800',
        gold: 'bg-gold-500 border-gold-700 text-emerald-100',
        cobalt: 'bg-cobalt-500 border-cobalt-700 text-emerald-100',
        cobaltDark: 'bg-cobalt-700 border-cobalt-900 text-emerald-100',
        mint: 'bg-mint-500 border-mint-700 text-mint-900',
        valid: 'bg-valid border-valid-foreground/40 text-valid-foreground',
        destructive:
          'bg-destructive border-destructive-foreground/40 text-destructive-foreground',
        info: 'bg-info border-info-foreground/40 text-info-foreground',
        warning:
          'bg-warning border-warning-foreground/40 text-warning-foreground',

        red: 'bg-red-background border-red-foreground/40 text-red-foreground',
        orange:
          'bg-orange-background border-orange-foreground/40 text-orange-foreground',
        amber:
          'bg-amber-background border-amber-foreground/40 text-amber-foreground',
        yellow:
          'bg-yellow-background border-yellow-foreground/40 text-yellow-foreground',
        lime: 'bg-lime-background border-lime-foreground/40 text-lime-foreground',
        green:
          'bg-green-background border-green-foreground/40 text-green-foreground',
        teal: 'bg-teal-background border-teal-foreground/40 text-teal-foreground',
        cyan: 'bg-cyan-background border-cyan-foreground/40 text-cyan-foreground',
        sky: 'bg-sky-background border-sky-foreground/40 text-sky-foreground',
        blue: 'bg-blue-background border-blue-foreground/40 text-blue-foreground',
        indigo:
          'bg-indigo-background border-indigo-foreground/40 text-indigo-foreground',
        violet:
          'bg-violet-background border-violet-foreground/40 text-violet-foreground',
        purple:
          'bg-purple-background border-purple-foreground/40 text-purple-foreground',
        fuchsia:
          'bg-fuchsia-background border-fuchsia-foreground/40 text-fuchsia-foreground',
        pink: 'bg-pink-background border-pink-foreground/40 text-pink-foreground',
        rose: 'bg-rose-background border-rose-foreground/40 text-rose-foreground',
        zinc: 'bg-zinc-background border-zinc-foreground/40 text-zinc-foreground',
      },
    },
    defaultVariants: {
      variant: 'primary',
    },
  }
);

const decoratorColorMap: Partial<
  Record<
    NonNullable<VariantProps<typeof badgeEyebrowVariants>['variant']>,
    string
  >
> = {
  emerald: 'text-emerald-400',
  lilac: 'text-lilac-600',
  gold: 'text-emerald-300',
  mint: 'text-mint-700',
  cobalt: 'text-emerald-300',
  cobaltDark: 'text-emerald-400',
  slate: 'text-emerald-600',
};

type TBadgeEyebrowProps = React.ComponentProps<'span'> &
  VariantProps<typeof badgeEyebrowVariants> & {
    asChild?: boolean;
    prefix?: string;
    suffix?: string;
    showPrefix?: boolean;
    showSuffix?: boolean;
  };

function BadgeEyebrow({
  className,
  variant = 'emerald',
  asChild = false,
  prefix = '_',
  suffix = '/',
  showPrefix = true,
  showSuffix = true,
  children,
  ...props
}: TBadgeEyebrowProps) {
  const Comp = asChild ? SlotPrimitive.Slot : 'span';
  const decoratorClass = decoratorColorMap[variant ?? 'emerald'];

  return (
    <Comp
      data-slot="badge-eyebrow"
      className={cn(badgeEyebrowVariants({ variant }), className)}
      {...props}
    >
      {showPrefix && (
        <span className={cn('text-xs tracking-none', decoratorClass)}>
          {prefix}
        </span>
      )}
      <span className="text-sm font-medium tracking-wide">{children}</span>
      {showSuffix && (
        <span className={cn('text-xs tracking-none', decoratorClass)}>
          {suffix}
        </span>
      )}
    </Comp>
  );
}

export { BadgeEyebrow, badgeEyebrowVariants };
