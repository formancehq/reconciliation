'use client';

/**
 * Compatibility wrapper over the DS Button.
 *
 * The ported code uses shadcn-default values (`variant="default"`,
 * `size="default"`, `size="icon"`); the Formance DS uses `primary`/`md`/
 * `icon-md`. This wrapper maps the legacy values so all call sites render the
 * DS button without per-call edits. All other DS variants/sizes pass through.
 */
import * as React from 'react';
import {
  Button as DSButton,
  buttonVariants,
  type TButtonProps as DSButtonProps,
} from '@workspace/ui/components/button';

type LegacyVariant = 'default';
type LegacySize = 'default' | 'icon';

export type ButtonProps = Omit<DSButtonProps, 'variant' | 'size'> & {
  variant?: NonNullable<DSButtonProps['variant']> | LegacyVariant;
  size?: NonNullable<DSButtonProps['size']> | LegacySize;
};

const mapVariant = (v: ButtonProps['variant']): DSButtonProps['variant'] =>
  (v === 'default' ? 'primary' : v) as DSButtonProps['variant'];

const mapSize = (s: ButtonProps['size']): DSButtonProps['size'] =>
  (s === 'default' ? 'md' : s === 'icon' ? 'icon-md' : s) as DSButtonProps['size'];

function Button({ variant, size, ...props }: ButtonProps) {
  return <DSButton variant={mapVariant(variant)} size={mapSize(size)} {...props} />;
}

export { Button, buttonVariants };
export type { ButtonProps as TButtonProps };
