import * as React from 'react';

import { cn } from '@workspace/ui/lib/utils';
import { cva, VariantProps } from 'class-variance-authority';

const cardVariants = cva(
  [
    'group/card relative rounded-lg bg-card text-card-foreground overflow-hidden border border-border',
    'dark:before:-inset-px dark:before:absolute dark:before:pointer-events-none',
  ],
  {
    variants: {
      variant: {
        muted: 'bg-muted/30',
      },
      isDisabled: {
        true: 'opacity-70 blur-xs pointer-events-none select-none',
      },
    },
    defaultVariants: {
      isDisabled: false,
    },
  }
);

type CardProps = React.HTMLAttributes<HTMLDivElement> &
  VariantProps<typeof cardVariants>;

function Card({ className, variant, isDisabled, ...props }: CardProps) {
  return (
    <div
      data-slot="card"
      className={cn(cardVariants({ variant, isDisabled, className }))}
      {...props}
    />
  );
}

function CardHeader({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="card-header"
      className={cn(
        '@container/card-header grid auto-rows-min grid-rows-[auto_auto] items-start gap-1.5 p-6 has-data-[slot=card-action]:grid-cols-[1fr_auto] [.border-b]:pb-6 pb-0',
        className
      )}
      {...props}
    />
  );
}

function CardTitle({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="card-title"
      className={cn('leading-none font-heading font-medium', className)}
      {...props}
    />
  );
}

function CardDescription({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="card-description"
      className={cn('text-muted-foreground text-sm', className)}
      {...props}
    />
  );
}

function CardAction({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="card-action"
      className={cn(
        'col-start-2 row-span-2 row-start-1 self-start justify-self-end',
        className
      )}
      {...props}
    />
  );
}

function CardContent({ className, ...props }: React.ComponentProps<'div'>) {
  return (
    <div
      data-slot="card-content"
      className={cn(
        'border-b p-6 last:border-none [&:has(+[data-slot=card-footer])]:border-none',
        className
      )}
      {...props}
    />
  );
}

const cardFooterVariant = cva('flex border-t p-4 px-6 justify-start', {
  variants: {
    variant: {
      isInformative:
        'items-center justify-between bg-muted text-muted-foreground overflow-hidden pb-3 pt-3',
      isDestructive:
        'items-center justify-between bg-destructive/50 text-destructive-foreground overflow-hidden pb-3 pt-3',
    },
  },
});

type CardFooterProps = React.HTMLAttributes<HTMLDivElement> &
  VariantProps<typeof cardFooterVariant>;

function CardFooter({ className, variant, ...props }: CardFooterProps) {
  return (
    <div
      data-slot="card-footer"
      className={cn(cardFooterVariant({ variant }), className)}
      {...props}
    />
  );
}

export {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
};
