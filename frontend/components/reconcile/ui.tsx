'use client';

/**
 * Small shared building blocks for the Reconcile surface: enum → DS Badge
 * mappers, a backend-health pill, and loading / empty / error states. Kept in
 * one place so every panel renders these identically.
 */
import type { ReactNode } from 'react';
import { Loader2, AlertTriangle, PlugZap, RefreshCw } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle,
} from '@workspace/ui/components/empty';
import { cn } from '@workspace/ui/lib/utils';
import {
  SEVERITY_META, STATUS_META, VERDICT_META, RESULT_META,
  type BadgeVariant,
} from '@/lib/recon';
import type { AlertStatus, EvaluationResult, Severity, Verdict } from '@/lib/recon';
import { ReconError } from '@/lib/recon';
import type { HealthStatus } from './ReconContext';

// ── Enum badges ───────────────────────────────────────────────────────────
function Tag({ variant, children }: { variant: BadgeVariant; children: ReactNode }) {
  return <Badge variant={variant}>{children}</Badge>;
}

export const SeverityBadge = ({ severity }: { severity: Severity }) => (
  <Tag variant={SEVERITY_META[severity].variant}>{SEVERITY_META[severity].label}</Tag>
);
export const StatusBadge = ({ status }: { status: AlertStatus }) => (
  <Tag variant={STATUS_META[status].variant}>{STATUS_META[status].label}</Tag>
);
export const VerdictBadge = ({ verdict }: { verdict: Verdict }) => (
  <Tag variant={VERDICT_META[verdict].variant}>{VERDICT_META[verdict].label}</Tag>
);
export const ResultBadge = ({ result }: { result: EvaluationResult }) => (
  <Tag variant={RESULT_META[result].variant}>{RESULT_META[result].label}</Tag>
);

// ── Health pill ─────────────────────────────────────────────────────────────
const HEALTH_META: Record<HealthStatus, { label: string; dot: string }> = {
  checking: { label: 'Checking…', dot: 'bg-muted-foreground animate-pulse' },
  up: { label: 'Connected', dot: 'bg-green-foreground' },
  down: { label: 'Unreachable', dot: 'bg-destructive' },
};

export function HealthPill({ health, onRetry }: { health: HealthStatus; onRetry?: () => void }) {
  const meta = HEALTH_META[health];
  return (
    <Button
      type="button"
      onClick={onRetry}
      title="Reconciliation backend (RECON_API_URL) — click to re-check"
      variant="outline"
      size="sm"
      className="h-7 gap-1.5 rounded-full px-2 text-xs font-normal text-muted-foreground"
    >
      <span className={cn('h-2 w-2 rounded-full', meta.dot)} />
      <span className="hidden sm:inline">Recon</span>
      <span>{meta.label}</span>
    </Button>
  );
}

// ── States ───────────────────────────────────────────────────────────────────
export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="flex h-full min-h-40 flex-col items-center justify-center gap-2 text-muted-foreground">
      <Loader2 className="h-6 w-6 animate-spin" />
      <p className="text-sm">{label}</p>
    </div>
  );
}

export function EmptyState({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <Empty className="h-full min-h-40 border-0 py-8 md:p-8">
      <EmptyHeader>
        {icon && <EmptyMedia variant="icon">{icon}</EmptyMedia>}
        <EmptyTitle className="text-sm">{title}</EmptyTitle>
        {children && <EmptyDescription>{children}</EmptyDescription>}
      </EmptyHeader>
    </Empty>
  );
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const isRecon = error instanceof ReconError;
  const unreachable = isRecon && error.isUnreachable;
  const title = unreachable ? 'Reconciliation backend unreachable' : isRecon ? `${error.errorCode}` : 'Something went wrong';
  const message = isRecon ? error.message : error instanceof Error ? error.message : String(error);
  const details = isRecon ? error.details : undefined;
  return (
    <div className="flex h-full min-h-40 flex-col items-center justify-center gap-3 px-6 text-center">
      {unreachable ? <PlugZap className="h-7 w-7 text-destructive-foreground" /> : <AlertTriangle className="h-7 w-7 text-amber-foreground" />}
      <div className="space-y-1">
        <p className="text-sm font-medium">{title}</p>
        <p className="max-w-md text-sm text-muted-foreground">{message}</p>
        {details && <p className="max-w-md font-mono text-xs text-muted-foreground/80">{details}</p>}
        {unreachable && (
          <p className="max-w-md text-xs text-muted-foreground">
            Start it with <code className="rounded bg-muted px-1">go run . serve --ledger-insecure --listen :8081</code>.
          </p>
        )}
      </div>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" /> Retry
        </Button>
      )}
    </div>
  );
}
