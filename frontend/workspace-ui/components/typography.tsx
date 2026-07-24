import { cn } from '@workspace/ui/lib/utils';
import { cva } from 'class-variance-authority';

export const h1Variants = cva([
  'scroll-m-20 text-4xl font-heading font-medium',
]);

export function TypographyH1({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'h1'>) {
  return (
    <h1 className={cn(h1Variants({ className }))} {...props}>
      {children}
    </h1>
  );
}

export const h2Variants = cva([
  'scroll-m-20 text-3xl font-heading font-medium first:mt-0',
]);

export function TypographyH2({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'h2'>) {
  return (
    <h2 className={cn(h2Variants({ className }))} {...props}>
      {children}
    </h2>
  );
}

export const h3Variants = cva([
  'scroll-m-20 text-2xl font-heading font-medium',
]);

export function TypographyH3({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'h3'>) {
  return (
    <h3 className={cn(h3Variants({ className }))} {...props}>
      {children}
    </h3>
  );
}

export const h4Variants = cva(['scroll-m-20 text-xl font-heading font-medium']);

export function TypographyH4({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'h4'>) {
  return (
    <h4 className={cn(h4Variants({ className }))} {...props}>
      {children}
    </h4>
  );
}

export const h5Variants = cva(['scroll-m-20 text-lg font-heading font-medium']);

export function TypographyH5({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'h5'>) {
  return (
    <h5 className={cn(h5Variants({ className }))} {...props}>
      {children}
    </h5>
  );
}
export function TypographyP({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'p'>) {
  return (
    <p className={cn('text-muted-foreground', className)} {...props}>
      {children}
    </p>
  );
}

export function TypographyBlockquote({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'blockquote'>) {
  return (
    <blockquote
      className={cn('mt-6 border-l-2 pl-6 italic', className)}
      {...props}
    >
      {children}
    </blockquote>
  );
}

export const TypographyListItem = ({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'li'>) => (
  <li className={cn('', className)} {...props}>
    {children}
  </li>
);

export function TypographyList({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'ul'>) {
  return (
    <ul className={cn('my-6 ml-6 list-disc [&>li]:mt-2', className)} {...props}>
      {children}
    </ul>
  );
}

export function TypographyInlineCode({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'code'>) {
  return (
    <code
      className={cn(
        'relative rounded bg-muted text-muted-foreground px-[0.3rem] py-[0.2rem] font-mono text-sm font-medium',
        className
      )}
      {...props}
    >
      {children}
    </code>
  );
}

export function TypographyLead({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'p'>) {
  return (
    <p className={cn('text-xl text-muted-foreground', className)} {...props}>
      {children}
    </p>
  );
}

export function TypographyLarge({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'div'>) {
  return (
    <div className={cn('text-lg', className)} {...props}>
      {children}
    </div>
  );
}

export function TypographySmall({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<'small'>) {
  return (
    <small className={cn('text-sm', className)} {...props}>
      {children}
    </small>
  );
}

export const TypographyBracketed = cva([
  "flex gap-1.5 before:content-['['] after:content-[']'] hover:text-foreground",
]);

export const TypographyMono = cva(['font-mono'], {
  variants: {
    uppercase: {
      true: 'uppercase',
    },
  },
  defaultVariants: { uppercase: false },
});
