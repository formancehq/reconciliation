import { cva, type VariantProps } from 'class-variance-authority';
import { Loader2 } from 'lucide-react';
import { Slot as SlotPrimitive } from 'radix-ui';
import React from 'react';
import { cn } from '@workspace/ui/lib/utils';
import { tabsListVariants } from '@workspace/ui/components/tabs';

export interface NavTabsProps extends React.HTMLAttributes<HTMLDivElement> {
  asChild?: boolean;
  ref?: React.Ref<HTMLDivElement>;
}

const NavTabs = ({
  className,
  asChild = false,
  ref,
  ...props
}: NavTabsProps) => {
  const Comp = asChild ? SlotPrimitive.Slot : 'nav';

  return (
    <Comp
      className={cn(
        tabsListVariants({ variant: 'line' }),
        'border-b border-border',
        className
      )}
      data-variant="line"
      ref={ref}
      role="tablist"
      {...props}
    />
  );
};
NavTabs.displayName = 'NavTabs';

const navTabVariants = cva(
  'group/navLink relative duration-200 text-sm font-medium pt-4 pb-3 border-b-2 border-transparent transition-all text-foreground cursor-pointer font-mono uppercase',
  {
    variants: {
      variant: {
        default: '',
        active: 'border-primary dark:border-gold-500 is-active',
        pending: 'is-pending',
        disabled: 'pointer-events-none',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  }
);

export interface NavTabProps
  extends
    React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof navTabVariants> {
  asChild?: boolean;
  isPending?: boolean;
  ref?: React.Ref<HTMLButtonElement>;
}

const NavTab = ({
  className,
  variant,
  asChild = false,
  isPending,
  ref,
  ...props
}: NavTabProps) => {
  const Comp = asChild ? SlotPrimitive.Slot : 'button';

  return (
    <Comp
      className={cn(navTabVariants({ variant, className }))}
      role="tab"
      ref={ref}
      {...props}
    >
      <div className="flex items-center gap-2">
        <span className="w-3"></span>
        <span className="block">{props.children}</span>
        <Loader2
          className={cn(
            'h-3 w-3 opacity-0',
            isPending && 'animate-spin opacity-100'
          )}
        />
      </div>
    </Comp>
  );
};
NavTab.displayName = 'NavTab';

export { NavTab, NavTabs };
