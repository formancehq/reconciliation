'use client';

import { CircleIcon } from 'lucide-react';
import { RadioGroup as RadioGroupPrimitive } from 'radix-ui';
import * as React from 'react';

import { cn } from '@workspace/ui/lib/utils';
import { Label } from '@workspace/ui/components/label';

function RadioGroupStacked({
  className,
  ...props
}: React.ComponentProps<typeof RadioGroupPrimitive.Root>) {
  return (
    <RadioGroupPrimitive.Root
      data-slot="radio-group-stacked"
      className={cn('flex w-full flex-col -space-y-px', className)}
      {...props}
    />
  );
}

type TRadioGroupStackedItemProps = {
  label: string;
  description?: string;
  showIndicator?: boolean;
  action?: React.ReactNode;
} & React.ComponentProps<typeof RadioGroupPrimitive.Item>;

function RadioGroupStackedItem({
  label,
  description,
  showIndicator = true,
  action,
  className,
  children,
  ...props
}: TRadioGroupStackedItemProps) {
  return (
    <RadioGroupPrimitive.Item
      data-slot="radio-group-stacked-item"
      className={cn(
        'flex w-full flex-col gap-2',
        'border bg-card/50 shadow-xs',
        'first-of-type:rounded-t-lg last-of-type:rounded-b-lg',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'enabled:cursor-pointer enabled:hover:bg-muted enabled:hover:border-muted-foreground',
        'hover:z-1 focus-visible:z-1 data-[state=checked]:z-1',
        'data-[state=checked]:ring-1 data-[state=checked]:ring-border',
        'data-[state=checked]:bg-muted data-[state=checked]:border-foreground/30',
        'transition group',
        className
      )}
      {...props}
    >
      <div className="flex w-full gap-3 px-[21px] py-3">
        {showIndicator && (
          <div
            className={cn(
              'relative flex aspect-square h-4 w-4 min-h-4 min-w-4 items-center justify-center',
              'rounded-full border transition',
              'ring-offset-background',
              'group-data-[state=checked]:border-foreground/50',
              'group-focus:border-primary group-focus:outline-none',
              'group-focus-visible:ring-2 group-focus-visible:ring-ring group-focus-visible:ring-offset-2',
              'group-hover:border-muted-foreground'
            )}
          >
            <RadioGroupPrimitive.Indicator className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2">
              <CircleIcon
                size={10}
                strokeWidth={0}
                className="fill-current text-current"
              />
            </RadioGroupPrimitive.Indicator>
          </div>
        )}
        <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
          <div className="flex items-baseline gap-2">
            <Label
              htmlFor={props.value}
              className={cn(
                'block text-left text-sm text-muted-foreground',
                'transition-colors',
                'enabled:group-hover:text-foreground group-data-[state=checked]:text-foreground'
              )}
            >
              {label}
            </Label>
            {action}
          </div>
          {description && (
            <p className="text-left text-sm text-balance text-muted-foreground/70">
              {description}
            </p>
          )}
          {children}
        </div>
      </div>
    </RadioGroupPrimitive.Item>
  );
}

export { RadioGroupStacked, RadioGroupStackedItem };
