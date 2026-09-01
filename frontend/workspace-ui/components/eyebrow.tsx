import { cva, type VariantProps } from 'class-variance-authority';
import { Slot as SlotPrimitive } from 'radix-ui';

import { cn } from '@workspace/ui/lib/utils';

const eyebrowVariants = cva(
  'pointer-events-none inline-flex font-mono font-medium uppercase w-fit shrink-0 items-center justify-center rounded-md text-xs whitespace-nowrap border-transparent',
  {
    variants: {
      variant: {
        primary: 'text-foreground',
        secondary: 'text-primary/80 dark:text-primary-foreground/80',
        gold: 'dark:text-gold-300 text-gold-500',
      },
      size: {
        xs: 'text-xs',
        sm: 'text-sm',
        md: 'text-base',
        lg: 'text-lg',
      },
    },
    defaultVariants: {
      variant: 'primary',
      size: 'md',
    },
  }
);

type TEyebrowProps = React.ComponentProps<'span'> &
  VariantProps<typeof eyebrowVariants> & {
    asChild?: boolean;
  };

function Eyebrow({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: TEyebrowProps) {
  const Comp = asChild ? SlotPrimitive.Slot : 'span';

  return (
    <Comp
      data-slot="eyebrow"
      className={cn(eyebrowVariants({ variant, size }), className)}
      {...props}
    />
  );
}

export { Eyebrow, eyebrowVariants, type TEyebrowProps };
