'use client';

import * as React from 'react';

import { cva, type VariantProps } from 'class-variance-authority';
import { EyeIcon, EyeOffIcon } from 'lucide-react';

import { cn } from '@workspace/ui/lib/utils';

import { Button } from '@workspace/ui/components/button';
import { Input } from '@workspace/ui/components/input';

const inputPasswordMaxWidthVariants = cva('', {
  variants: {
    maxWidth: {
      xs: 'max-w-24 md:max-w-32 lg:max-w-40',
      sm: 'max-w-32 md:max-w-64 lg:max-w-56',
      md: 'max-w-44 md:max-w-64 lg:max-w-96',
      lg: 'max-w-44 md:max-w-64 lg:max-w-120',
      xl: 'max-w-44 md:max-w-64 lg:max-w-165',
    },
  },
});

type InputPasswordProps = Omit<
  React.InputHTMLAttributes<HTMLInputElement>,
  'type' | 'size'
> &
  VariantProps<typeof inputPasswordMaxWidthVariants>;

function InputPassword({ className, maxWidth, ...props }: InputPasswordProps) {
  const [showPassword, setShowPassword] = React.useState(false);
  const disabled =
    props.value === '' || props.value === undefined || props.disabled;

  return (
    <div
      className={cn('relative', inputPasswordMaxWidthVariants({ maxWidth }))}
    >
      <Input
        type={showPassword ? 'text' : 'password'}
        className={cn('hide-password-toggle pr-10 font-mono', className)}
        {...props}
      />
      <Button
        type="button"
        variant={showPassword ? 'ghost' : 'outline'}
        size="icon-xs"
        className="absolute right-1 top-1/2 -translate-y-1/2"
        onClick={() => setShowPassword((prev) => !prev)}
        disabled={disabled}
      >
        {showPassword && !disabled ? (
          <EyeIcon aria-hidden="true" />
        ) : (
          <EyeOffIcon aria-hidden="true" />
        )}
        <span className="sr-only">
          {showPassword ? 'Hide password' : 'Show password'}
        </span>
      </Button>

      {/* hides browsers password toggles */}
      <style>{`
        .hide-password-toggle::-ms-reveal,
        .hide-password-toggle::-ms-clear {
          visibility: hidden;
          pointer-events: none;
          display: none;
        }
      `}</style>
    </div>
  );
}

export { InputPassword };
