'use client';

import { useTheme } from 'next-themes';
import { Toaster as Sonner, ToasterProps } from 'sonner';

const SonnerToaster = ({ ...props }: ToasterProps) => {
  const { theme = 'system' } = useTheme();

  return (
    <Sonner
      theme={theme as ToasterProps['theme']}
      className="toaster group"
      toastOptions={{
        classNames: {
          toast: 'bg-background! text-foreground! border-border! shadow-lg!',
          description: 'text-muted-foreground!',
          actionButton: 'text-primary-foreground!',
          cancelButton: 'text-muted-foreground!',
          success: 'text-valid-foreground!',
          error: 'text-destructive-foreground!',
          warning: 'text-warning-foreground!',
          info: 'text-cobalt-600!',
        },
      }}
      {...props}
    />
  );
};

export { SonnerToaster };
