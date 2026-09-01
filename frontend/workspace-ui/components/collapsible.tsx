'use client';

import { Collapsible as CollapsiblePrimitive } from 'radix-ui';
import * as React from 'react';

type CollapsibleProps = {
  open?: boolean;
  defaultOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
  disabled?: boolean;
  children?: React.ReactNode;
  className?: string;
} & Omit<React.HTMLAttributes<HTMLDivElement>, 'children' | 'className'>;

type CollapsibleTriggerProps = {
  asChild?: boolean;
  children?: React.ReactNode;
  className?: string;
} & Omit<
  React.ButtonHTMLAttributes<HTMLButtonElement>,
  'children' | 'className'
>;

type CollapsibleContentProps = {
  forceMount?: boolean;
  children?: React.ReactNode;
  className?: string;
} & Omit<React.HTMLAttributes<HTMLDivElement>, 'children' | 'className'>;

function Collapsible(props: CollapsibleProps) {
  return <CollapsiblePrimitive.Root data-slot="collapsible" {...props} />;
}

function CollapsibleTrigger(props: CollapsibleTriggerProps) {
  return (
    <CollapsiblePrimitive.CollapsibleTrigger
      data-slot="collapsible-trigger"
      {...props}
    />
  );
}

function CollapsibleContent(props: CollapsibleContentProps) {
  const { forceMount, ...contentProps } = props;

  return (
    <CollapsiblePrimitive.CollapsibleContent
      data-slot="collapsible-content"
      {...contentProps}
    />
  );
}

export { Collapsible, CollapsibleContent, CollapsibleTrigger };
