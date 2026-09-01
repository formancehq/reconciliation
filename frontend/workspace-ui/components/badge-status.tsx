import { cva, type VariantProps } from 'class-variance-authority';
import type { ComponentProps } from 'react';

import { Badge } from '@workspace/ui/components/badge';
import { cn } from '@workspace/ui/lib/utils';

const badgeStatusIndicatorVariants = cva(
  'absolute inline-flex h-full w-full animate-ping opacity-75',
  {
    variants: {
      variant: {
        valid: 'bg-emerald-500',
        destructive: 'bg-red-500',
        info: 'bg-blue-500',
        warning: 'bg-amber-500',
        zinc: 'bg-zinc-500',
        outline: 'bg-foreground',
      },
    },
    defaultVariants: {
      variant: 'zinc',
    },
  }
);

const badgeStatusIndicatorSolidVariants = cva('relative inline-flex h-2 w-2', {
  variants: {
    variant: {
      valid: 'bg-emerald-500',
      destructive: 'bg-red-500',
      info: 'bg-blue-500',
      warning: 'bg-amber-500',
      zinc: 'bg-zinc-500',
      outline: 'bg-foreground',
    },
  },
  defaultVariants: {
    variant: 'zinc',
  },
});

const badgeStatusLabelVariants = cva('', {
  variants: {
    variant: {
      valid: 'text-valid-foreground',
      destructive: 'text-destructive-foreground',
      info: 'text-info-foreground',
      warning: 'text-warning-foreground',
      zinc: 'text-zinc-foreground',
      outline: 'text-foreground',
    },
  },
});

export type TBadgeStatusProps = ComponentProps<typeof Badge> &
  VariantProps<typeof badgeStatusIndicatorVariants>;

export const BadgeStatus = ({
  className,
  variant = 'zinc',
  children,
  ...props
}: TBadgeStatusProps) => (
  <Badge
    className={cn('flex items-center gap-2', className)}
    variant={variant}
    {...props}
  >
    <span className="relative flex h-2 w-2">
      <span className={badgeStatusIndicatorVariants({ variant })} />
      <span className={badgeStatusIndicatorSolidVariants({ variant })} />
    </span>
    <span className={badgeStatusLabelVariants({ variant })}>{children}</span>
  </Badge>
);
