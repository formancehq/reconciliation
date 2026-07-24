import { cn } from '@workspace/ui/lib/utils';
import { Separator } from '@workspace/ui/components/separator';

export function DescriptionList({
  className,
  ...props
}: React.ComponentPropsWithoutRef<'dl'>) {
  return (
    <dl
      {...props}
      className={cn('grid grid-cols-1 sm:grid-cols-3', className)}
    />
  );
}

export function DescriptionTerm({
  className,
  ...props
}: React.ComponentPropsWithoutRef<'dt'>) {
  return (
    <dt
      {...props}
      className={cn(
        'col-start-1 text-sm border-t border-border pt-4 first:border-none sm:border-t sm:py-2',
        className
      )}
    />
  );
}

export function DescriptionDetails({
  className,
  ...props
}: React.ComponentPropsWithoutRef<'dd'>) {
  return (
    <dd
      {...props}
      className={cn(
        'col-span-2 pb-3 pt-1 font-mono sm:border-t sm:border-border sm:py-4 sm:nth-2:border-none',
        className
      )}
    />
  );
}

export function DescriptionRow({
  className,
  ...props
}: React.ComponentPropsWithoutRef<'div'>) {
  return <div {...props} className={cn('flex gap-6', className)} />;
}

export function DescriptionRowGroup({
  className,
  ...props
}: React.ComponentPropsWithoutRef<'div'>) {
  return <div {...props} className={cn('space-y-1', className)} />;
}

export function DescriptionRowSeparator() {
  return (
    <div>
      <Separator orientation="vertical" />
    </div>
  );
}
