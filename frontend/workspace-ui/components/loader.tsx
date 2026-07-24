import React from 'react';

import { Skeleton } from '@workspace/ui/components/skeleton';
import { cn } from '@workspace/ui/lib/utils';

type TLoaderProps = {
  fullscreen?: boolean;
} & React.HTMLAttributes<HTMLDivElement>;

function Loader({ children, className }: TLoaderProps) {
  return (
    <div className={cn('', className)}>
      <div>
        <div className="flex items-center space-x-4">
          <Skeleton className="h-12 w-12 rounded-full" />
          <div className="space-y-2">
            <Skeleton className="h-4 w-[250px]" />
            <Skeleton className="h-4 w-[230px]" />
            <Skeleton className="h-4 w-[220px]" />
            <Skeleton className="h-4 w-[210px]" />
          </div>
        </div>
        {children && <div className="mt-4">{children}</div>}
      </div>
    </div>
  );
}

export { Loader };
export type { TLoaderProps };
