'use client';

import { Popover as PopoverPrimitive } from 'radix-ui';
import * as React from 'react';

import { cn } from '@workspace/ui/lib/utils';
import { cva, VariantProps } from 'class-variance-authority';

function Popover({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Root>) {
  return <PopoverPrimitive.Root data-slot="popover" {...props} />;
}

export const popoverTriggerVariants = cva('flex items-center justify-between', {
  variants: {
    size: {
      sm: 'h-7',
      md: 'h-8',
      lg: 'h-9',
    },
  },
  defaultVariants: {
    size: 'md',
  },
});

export type PopoverTriggerProps = React.ComponentProps<
  typeof PopoverPrimitive.Trigger
> &
  VariantProps<typeof popoverTriggerVariants>;

function PopoverTrigger({ className, size, ...props }: PopoverTriggerProps) {
  return (
    <PopoverPrimitive.Trigger
      data-slot="popover-trigger"
      className={cn(popoverTriggerVariants({ size, className }))}
      {...props}
    />
  );
}

export const popoverContentVariants = cva(
  'z-50 w-72 border border-border bg-popover p-4 text-popover-foreground shadow-md outline-none data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2',
  {
    variants: {
      size: {
        sm: 'w-48',
        md: 'w-64',
        lg: 'w-80',
      },
    },
    defaultVariants: {
      size: 'md',
    },
  }
);

type PopoverContentProps = React.ComponentProps<
  typeof PopoverPrimitive.Content
> &
  VariantProps<typeof popoverContentVariants>;

function PopoverContent({
  className,
  size,
  align = 'center',
  sideOffset = 4,
  ...props
}: PopoverContentProps) {
  return (
    <PopoverPrimitive.Portal>
      <PopoverPrimitive.Content
        data-slot="popover-content"
        align={align}
        sideOffset={sideOffset}
        className={cn(popoverContentVariants({ size, className }))}
        {...props}
      />
    </PopoverPrimitive.Portal>
  );
}

function PopoverAnchor({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Anchor>) {
  return <PopoverPrimitive.Anchor data-slot="popover-anchor" {...props} />;
}

const PopoverClose = PopoverPrimitive.Close;

export { Popover, PopoverAnchor, PopoverClose, PopoverContent, PopoverTrigger };
