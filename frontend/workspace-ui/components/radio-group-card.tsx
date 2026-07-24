'use client';

import { RadioGroup as RadioGroupPrimitive } from 'radix-ui';
import { CircleIcon } from 'lucide-react';
import * as React from 'react';

import { cn } from '@workspace/ui/lib/utils';

function RadioGroupCard({
  className,
  ...props
}: React.ComponentProps<typeof RadioGroupPrimitive.Root>) {
  return (
    <RadioGroupPrimitive.Root
      data-slot="radio-group-card"
      className={cn('grid gap-2', className)}
      {...props}
    />
  );
}

type TRadioGroupCardItemProps = {
  label: string | React.ReactNode;
  showIndicator?: boolean;
} & React.ComponentProps<typeof RadioGroupPrimitive.Item>;

function RadioGroupCardItem({
  label,
  showIndicator = true,
  className,
  children,
  ...props
}: TRadioGroupCardItemProps) {
  return (
    <RadioGroupPrimitive.Item
      data-slot="radio-group-card-item"
      className={cn(
        'flex flex-col gap-2 w-48',
        'bg-card rounded-md border p-2',
        'hover:border-muted-foreground',
        'hover:z-[1] focus-visible:z-[1]',
        'data-[state=checked]:z-[1]',
        'data-[state=checked]:ring-1 data-[state=checked]:ring-border',
        'data-[state=checked]:bg-muted',
        'data-[state=checked]:border-foreground/30',
        'transition-colors',
        'group',
        'disabled:cursor-not-allowed disabled:opacity-50',
        className
      )}
      {...props}
    >
      {children}
      <label className="flex w-full gap-2" htmlFor={props.value}>
        {showIndicator && (
          <div
            className={cn(
              'flex aspect-square h-4 w-4 items-center justify-center',
              'rounded-full border transition',
              'ring-offset-background',
              'group-data-[state=checked]:border-foreground/50',
              'group-focus:border-primary group-focus:outline-none',
              'group-focus-visible:ring-2 group-focus-visible:ring-ring group-focus-visible:ring-offset-2',
              'group-hover:border-muted-foreground',
              'group-disabled:cursor-not-allowed group-disabled:opacity-50'
            )}
          >
            <RadioGroupPrimitive.Indicator className="flex items-center justify-center">
              <CircleIcon className="h-2.5 w-2.5 fill-current text-current" />
            </RadioGroupPrimitive.Indicator>
          </div>
        )}
        <div
          className={cn(
            'w-full text-left text-xs transition-colors',
            'text-muted-foreground',
            'group-hover:text-foreground group-data-[state=checked]:text-foreground',
            props.disabled ? 'cursor-not-allowed' : 'cursor-pointer'
          )}
        >
          {label}
        </div>
      </label>
    </RadioGroupPrimitive.Item>
  );
}

export { RadioGroupCard, RadioGroupCardItem };
