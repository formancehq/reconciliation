'use client';

/**
 * Compatibility wrapper over the DS Badge: maps the legacy `variant="default"`
 * to the DS `primary`. All other DS variants pass through unchanged.
 */
import {
  Badge as DSBadge,
  badgeVariants,
  type TBadgeProps as DSBadgeProps,
} from '@workspace/ui/components/badge';

export type BadgeProps = Omit<DSBadgeProps, 'variant'> & {
  variant?: NonNullable<DSBadgeProps['variant']> | 'default';
};

function Badge({ variant, ...props }: BadgeProps) {
  return <DSBadge variant={(variant === 'default' ? 'primary' : variant) as DSBadgeProps['variant']} {...props} />;
}

export { Badge, badgeVariants };
export type { BadgeProps as TBadgeProps };
