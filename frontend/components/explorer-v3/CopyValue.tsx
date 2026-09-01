'use client';

/**
 * Click-to-copy value with a rich tooltip, shared by the V3 explorer tabs
 * (transaction references/amounts/timestamps, account balances, metadata keys).
 * Built on the resilient clipboardHelper (Clipboard API + execCommand fallback).
 */

import { type ReactNode } from 'react';
import { copyToClipboard } from '@/lib/clipboardHelper';
import { toast } from '@/components/ui/toast';
import { cn } from '@/lib/utils';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

/** Copy `text` to the clipboard and toast the outcome. */
export async function copyWithToast(text: string): Promise<void> {
  const ok = await copyToClipboard(text);
  if (ok) toast.success('Copied to clipboard');
  else toast.error('Could not copy to clipboard');
}

export function CopyValue({
  text, className, tooltipSide = 'top', children,
}: {
  /** The exact text placed on the clipboard (and shown in the tooltip). */
  text: string;
  className?: string;
  tooltipSide?: 'top' | 'bottom' | 'left' | 'right';
  /** What to render; defaults to `text`. */
  children?: ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={() => copyWithToast(text)}
          aria-label={`Copy ${text}`}
          className={cn('cursor-copy rounded px-1 text-left transition-colors hover:bg-primary/10', className)}
        >
          {children ?? text}
        </button>
      </TooltipTrigger>
      <TooltipContent side={tooltipSide} className="max-w-[400px] break-all font-mono text-xs">
        {text}
        <span className="mt-1 block font-sans text-[10px] text-muted-foreground">Click to copy</span>
      </TooltipContent>
    </Tooltip>
  );
}
