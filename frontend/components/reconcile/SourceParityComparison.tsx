'use client';

import { AlertTriangle, BookOpen, Braces, Equal, Filter, Layers3 } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Card } from '@/components/ui/card';
import { Alert, AlertDescription, AlertTitle } from '@workspace/ui/components/alert';
import { cn } from '@workspace/ui/lib/utils';
import { parseParitySide } from '@/lib/recon/paritySpec';
import { parseReconQuery, type MetaRule } from '@/lib/recon/querySpec';

type Density = 'compact' | 'detail';

interface Props {
  spec: unknown;
  density?: Density;
  /** Editor previews may contain intentionally incomplete values. */
  preview?: boolean;
}

interface SideView {
  kind: 'ledger' | 'account_metadata';
  ledger: string;
  address: string;
  filters: MetaRule[];
  filterCombinator: 'and' | 'or';
  metadataKey: string;
  asset: string;
}

const operatorLabel: Record<string, string> = {
  '=': 'equals', '>': 'is greater than', '>=': 'is at least', '<': 'is less than', '<=': 'is at most',
  between: 'is between', exists: 'exists', '~': 'starts with',
};

function sideView(raw: unknown): SideView {
  const value = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>;
  const parsed = parseParitySide(value);
  const query = parseReconQuery(value.query);
  return {
    kind: parsed.kind,
    ledger: typeof value.ledger === 'string' ? value.ledger : '',
    address: query.address,
    filters: query.meta,
    filterCombinator: query.metaComb,
    metadataKey: parsed.metadataKey,
    asset: parsed.asset,
  };
}

function valuePath(side: SideView): string {
  if (side.kind === 'ledger') return 'Account balance formed from postings';
  if (side.metadataKey) return `account.metadata["${side.metadataKey}"]`;
  return 'account.metadata["<metadata key>"]';
}

function filterText(rule: MetaRule): string {
  const value = rule.op === 'exists' ? '' : ` ${rule.value}${rule.op === 'between' ? ` and ${rule.value2 ?? ''}` : ''}`;
  return `${rule.key || 'metadata key'} ${operatorLabel[rule.op] ?? rule.op}${value}`;
}

function SourceCard({ label, side, density, preview }: { label: string; side: SideView; density: Density; preview: boolean }) {
  const isLedger = side.kind === 'ledger';
  const Icon = isLedger ? BookOpen : Braces;
  const kindLabel = isLedger ? 'Posting-derived balance' : 'Account metadata balance';
  const address = side.address || (preview ? 'Choose an account or pattern' : '* (all accounts)');
  const ledger = side.ledger || (preview ? 'Choose a ledger' : 'Not specified');

  return (
    <Card className={cn('min-w-0 border-border/80 shadow-none', density === 'compact' && 'rounded-md')}>
      <div className={cn('flex items-start gap-2.5 border-b bg-muted/30', density === 'detail' ? 'p-3' : 'px-2.5 py-2')}>
        <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground">
          <Icon className="h-3.5 w-3.5" />
        </span>
        <div className="min-w-0">
          <p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{label}</p>
          <p className="text-xs font-semibold text-foreground">{kindLabel}</p>
        </div>
      </div>

      <dl className={cn('grid min-w-0', density === 'detail' ? 'gap-3 p-3 sm:grid-cols-2' : 'gap-2 p-2.5')}>
        <Fact label="Ledger" value={ledger} mono />
        <Fact label="Account selector" value={address} mono />
        <Fact label="Value read" value={valuePath(side)} mono={!isLedger} className={density === 'detail' ? 'sm:col-span-2' : undefined} />
        {!isLedger && (
          <>
            <Fact
              label="Represents"
              value={side.asset ? `${side.asset} balance, stored as an integer in minor units` : 'Declared asset required'}
              className={density === 'detail' ? 'sm:col-span-2' : undefined}
            />
            {density === 'detail' && (
              <p className="sm:col-span-2 text-xs leading-relaxed text-muted-foreground">
                This reads a value already stored on the account object. It does not calculate a balance from postings.
              </p>
            )}
          </>
        )}
        {isLedger && density === 'detail' && (
          <p className="sm:col-span-2 text-xs leading-relaxed text-muted-foreground">
            Formance calculates this balance from the postings on every account matched by the selector.
          </p>
        )}
        {side.filters.length > 0 && (
          <div className={cn('min-w-0', density === 'detail' && 'sm:col-span-2')}>
            <dt className="mb-1 flex items-center gap-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
              <Filter className="h-3 w-3" /> Account metadata filter
            </dt>
            <dd className="space-y-0.5 text-xs text-foreground">
              {side.filters.map((rule, index) => (
                <p key={`${rule.key}-${index}`} className="break-words">
                  {index > 0 && <span className="mr-1 text-muted-foreground">{side.filterCombinator.toUpperCase()}</span>}
                  {filterText(rule)}
                </p>
              ))}
            </dd>
          </div>
        )}
      </dl>
    </Card>
  );
}

function Fact({ label, value, mono, className }: { label: string; value: string; mono?: boolean; className?: string }) {
  return (
    <div className={cn('min-w-0', className)}>
      <dt className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{label}</dt>
      <dd className={cn('mt-0.5 break-words text-xs text-foreground', mono && 'font-mono')}>{value}</dd>
    </div>
  );
}

export function SourceParityComparison({ spec, density = 'detail', preview = false }: Props) {
  const value = (spec && typeof spec === 'object' ? spec : {}) as Record<string, unknown>;
  const left = sideView(value.left);
  const right = sideView(value.right);
  const scope = value.scope === 'per_account' ? 'Per matching account' : 'Combined across matching accounts';
  const tolerance = Object.entries((value.tolerance && typeof value.tolerance === 'object' ? value.tolerance : {}) as Record<string, unknown>);
  const toleranceLabel = tolerance.length === 0
    ? 'Exact match'
    : tolerance.map(([asset, amount]) => `${asset}: ±${String(amount)} minor units`).join(' · ');
  const allowsDeviation = tolerance.some(([, amount]) => {
    const numericAmount = typeof amount === 'number' ? amount : Number(amount);
    return !Number.isFinite(numericAmount) || numericAmount !== 0;
  });
  const metadataSides = [left, right].filter((side) => side.kind === 'account_metadata');
  const metadataMismatch = left.kind === 'account_metadata' && right.kind === 'account_metadata'
    && !!left.asset && !!right.asset && left.asset !== right.asset;
  const assetWarnings = metadataSides.flatMap((side) => tolerance
    .filter(([asset]) => !!side.asset && asset !== side.asset)
    .map(([asset]) => `${side.metadataKey || 'The metadata key'} maps to ${side.asset}, while tolerance is configured for ${asset}.`));
  if (metadataMismatch) {
    assetWarnings.unshift(`Source A maps ${left.metadataKey || 'its metadata key'} to ${left.asset}, while Source B maps ${right.metadataKey || 'its metadata key'} to ${right.asset}. Account metadata sources must declare the same asset.`);
  }
  const hasMetadata = metadataSides.length > 0;

  return (
    <section aria-label="Rule comparison" className={cn('min-w-0', density === 'detail' && 'space-y-3')}>
      {density === 'detail' && (
        <div className="flex items-start gap-2">
          <Layers3 className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
          <div>
            <h3 className="text-sm font-semibold">What this rule compares</h3>
            <p className="text-xs text-muted-foreground">{hasMetadata ? 'This rule checks the metadata source’s declared asset. A difference outside the allowed tolerance creates a break.' : 'Both sources are compared for each asset. A difference outside the allowed tolerance creates a break.'}</p>
          </div>
        </div>
      )}

      <div className="grid min-w-0 items-stretch gap-2 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
        <SourceCard label="Source A" side={left} density={density} preview={preview} />
        <div className="flex items-center justify-center gap-2 py-0.5 text-muted-foreground md:flex-col md:px-0.5">
          <span className="h-px flex-1 bg-border md:h-auto md:w-px" />
          <span className="flex h-7 shrink-0 items-center gap-1 rounded-full border bg-background px-2 text-[10px] font-semibold uppercase tracking-wide">
            {allowsDeviation
              ? <span className="text-sm leading-none" aria-hidden="true">≈</span>
              : <Equal className="h-3.5 w-3.5" aria-hidden="true" />}
            {allowsDeviation ? 'within tolerance' : 'exact match'}
          </span>
          <span className="h-px flex-1 bg-border md:h-auto md:w-px" />
        </div>
        <SourceCard label="Source B" side={right} density={density} preview={preview} />
      </div>

      <div className={cn('flex flex-wrap items-center gap-1.5', density === 'detail' ? 'rounded-md border bg-muted/20 px-3 py-2' : 'mt-2')}>
        <Badge variant="outline" size="sm">{scope}</Badge>
        <Badge variant="outline" size="sm">{toleranceLabel}</Badge>
        {density === 'detail' && (
          <span className="text-xs text-muted-foreground">
            {hasMetadata ? 'This rule checks the declared asset; additional assets require additional rules.' : 'Values are compared per asset.'}
          </span>
        )}
      </div>
      {assetWarnings.length > 0 && (
        <Alert variant="warning" className={cn(density === 'compact' && 'mt-2 px-3 py-2')}>
          <AlertTriangle />
          <AlertTitle className="text-xs">{metadataMismatch ? 'Metadata assets do not match' : 'Asset mapping needs review'}</AlertTitle>
          <AlertDescription className="text-xs">
            {assetWarnings.map((warning) => <p key={warning}>{warning}</p>)}
          </AlertDescription>
        </Alert>
      )}
    </section>
  );
}
