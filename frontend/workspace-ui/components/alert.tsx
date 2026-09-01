import { cva, type VariantProps } from 'class-variance-authority';
import * as React from 'react';

import { cn } from '@workspace/ui/lib/utils';

const alertVariants = cva(
  'relative w-full rounded-lg border px-4 py-3 text-sm grid has-[>svg]:grid-cols-[calc(var(--spacing)*4)_1fr] grid-cols-[0_1fr] has-[>svg]:gap-x-3 gap-y-0.5 items-start [&>svg]:size-4 [&>svg]:translate-y-0.5 [&>svg]:text-current',
  {
    variants: {
      variant: {
        default: 'bg-card text-card-foreground',
        destructive:
          'text-destructive-foreground bg-card [&>svg]:text-current *:data-[slot=alert-description]:text-destructive-foreground/90',
        info: 'text-info-foreground bg-card [&>svg]:text-current *:data-[slot=alert-description]:text-info-foreground/90',
        warning:
          'text-warning-foreground bg-card [&>svg]:text-current *:data-[slot=alert-description]:text-warning-foreground/90',
        success:
          'text-success-foreground bg-card [&>svg]:text-current *:data-[slot=alert-description]:text-success-foreground/90',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
);

function Alert({
  className,
  variant,
  ...props
}: React.ComponentProps<'div'> & VariantProps<typeof alertVariants>) {
  return (
    <div
      data-slot="alert"
      role="alert"
      className={cn(alertVariants({ variant }), className)}
      {...props}
    />
  );
}

function AlertTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="alert-title"
      className={cn(
        'col-start-2 line-clamp-1 min-h-4 font-medium text-lg tracking-tight',
        className
      )}
      {...props}
    />
  );
}

function AlertDescription({
  className,
  ...props
}: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="alert-description"
      className={cn(
        'text-muted-foreground col-start-2 grid justify-items-start gap-1 text-sm [&_p]:leading-relaxed',
        className
      )}
      {...props}
    />
  );
}

function AlertContent({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="alert-content"
      className={cn('col-span-2 flex flex-col gap-2 text-sm pt-3', className)}
      {...props}
    />
  );
}

export { Alert, AlertContent, AlertDescription, AlertTitle };
