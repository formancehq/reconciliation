'use client';

/**
 * One lettering run: its verdict, the reconciliation statement from its manifest
 * (bridge, open items, open books, payment-account book: results doc §5), and
 * its breaks file. Layout and wording follow the presentation of results doc §10.
 */
import { Fragment, type ReactNode } from 'react';
import { AlertTriangle } from 'lucide-react';
import { Card } from '@/components/ui/card';
import { useReconResource } from '@/lib/recon';
import { readRun, type RunResults } from '@/lib/lettering/read';
import { assetCode, formatDay, words } from '@/lib/lettering/format';
import type { AssetStatement, Book, Manifest, PaymentAccount, StatementLine } from '@/lib/lettering/manifest';
import { ErrorState, Loading } from '../ui';
import { BreaksTable } from './BreaksTable';
import {
  Amount,
  DataTable,
  CheckMark,
  EmptyNote,
  LetteringVerdictBadge,
  OutcomeBadge,
  SectionTitle,
  Td,
  Th,
  THead,
} from './ui';

export function RunView({ path }: { path: string }) {
  const res = useReconResource<RunResults>(() => readRun(path), [path]);
  if (res.loading) return <Loading label="Reading the run with DuckDB…" />;
  if (res.error) return <ErrorState error={res.error} onRetry={res.refetch} />;
  if (!res.data) return null;
  const { manifest, breaks, wrongSign } = res.data;

  return (
    <div className="space-y-6">
      <RunHeader manifest={manifest} />
      {manifest.verdict === 'incomplete' ? (
        <IncompleteNotice manifest={manifest} />
      ) : (
        <>
          {Object.entries(manifest.statement ?? {}).map(([asset, s]) => (
            <StatementCard key={asset} asset={asset} statement={s} previousDay={manifest.previousRun?.day} />
          ))}
          <BooksSection books={manifest.books ?? []} wrongSign={wrongSign ?? []} />
          <PaymentAccountsSection accounts={manifest.paymentAccounts ?? []} />
          {breaks && <BreaksTable breaks={breaks} />}
        </>
      )}
    </div>
  );
}

// ── Header ───────────────────────────────────────────────────────────────────

function RunHeader({ manifest: m }: { manifest: Manifest }) {
  const b = m.counts?.breaks;
  const open = b ? Object.values(b.openByLeg).reduce((sum, n) => sum + n, 0) : undefined;
  const legs = b
    ? Object.entries(b.openByLeg)
        .filter(([, n]) => n > 0)
        .map(([leg, n]) => `${n} ${leg}`)
        .join(', ')
    : '';
  const unclassified = m.counts?.unclassified
    ? Object.values(m.counts.unclassified).reduce((sum, n) => sum + n, 0)
    : undefined;

  return (
    <Card className="gap-3 p-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <LetteringVerdictBadge verdict={m.verdict} size="lg" />
        <h2 className="text-base font-semibold">
          {m.rule.id} <span className="text-muted-foreground">·</span> {formatDay(m.period.day)}
        </h2>
      </div>
      <p className="text-xs break-words text-muted-foreground">
        Cut-off <span className="font-mono">{m.period.cutoff}</span> ({m.period.tz}) · run{' '}
        <span className="font-mono">{m.runId}</span> ·{' '}
        {m.previousRun ? (
          <>
            previous run {formatDay(m.previousRun.day)} (<span className="font-mono">{m.previousRun.runId}</span>)
          </>
        ) : (
          <>first run{m.rule.backfillFrom ? `, open items seeded since ${formatDay(m.rule.backfillFrom)}` : ''}</>
        )}
        {m.engine ? ` · ${m.engine}` : ''}
      </p>
      {m.verdict !== 'incomplete' && (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Figure label="Open breaks" value={open} hint={legs || 'none'} alert={!!open} />
          <Figure label="At P1" value={b?.openByPriority['1'] ?? 0} alert={!!b?.openByPriority['1']} />
          <Figure
            label="Pending"
            value={m.counts?.flowOutcome?.pending}
            hint="not breaks yet"
          />
          <Figure
            label="Unclassified"
            value={unclassified}
            hint={
              m.counts?.unclassified
                ? Object.entries(m.counts.unclassified)
                    .map(([side, n]) => `${n} ${side}`)
                    .join(', ')
                : undefined
            }
            alert={!!unclassified}
          />
        </div>
      )}
    </Card>
  );
}

function Figure({ label, value, hint, alert }: { label: string; value?: number; hint?: string; alert?: boolean }) {
  return (
    <div className="rounded-md border px-3 py-2">
      <div className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">{label}</div>
      <div className={alert ? 'text-xl font-semibold text-destructive-foreground tabular-nums' : 'text-xl font-semibold tabular-nums'}>
        {value ?? '—'}
      </div>
      {hint && <div className="truncate text-[11px] text-muted-foreground">{hint}</div>}
    </div>
  );
}

function IncompleteNotice({ manifest: m }: { manifest: Manifest }) {
  return (
    <Card className="gap-2 border-amber-foreground/30 bg-amber-background/40 p-4">
      <div className="flex items-center gap-2 text-sm font-medium text-amber-foreground">
        <AlertTriangle className="h-4 w-4" />
        No conclusion: {words(m.incomplete?.reason ?? 'unknown reason')}
      </div>
      {m.incomplete?.detail && <p className="text-xs break-words">{m.incomplete.detail}</p>}
      <p className="text-xs text-muted-foreground">
        An incomplete run writes a reduced manifest and no data file. The day&apos;s data is in its current run.
      </p>
    </Card>
  );
}

// ── Statement ────────────────────────────────────────────────────────────────

/** The heading of a part of the statement: Bridge, Open items, Unclassified. */
const EYEBROW = 'mb-1 text-xs font-semibold tracking-wide text-muted-foreground uppercase';

/** One row of a statement: label, amount, count and chips, with an operator in front. */
function Row({
  op,
  label,
  chips,
  amount,
  count,
  asset,
  signed,
  strong,
  check,
  indent,
}: {
  op?: string;
  label: ReactNode;
  chips?: ReactNode;
  amount: string;
  count?: number;
  asset: string;
  signed?: boolean;
  strong?: boolean;
  check?: boolean;
  indent?: boolean;
}) {
  return (
    <div className={strong ? 'flex items-baseline gap-2 py-1 font-medium' : 'flex items-baseline gap-2 py-1'}>
      <span className="w-3 shrink-0 font-mono text-muted-foreground">{op}</span>
      <span className={indent ? 'min-w-0 flex-1 pl-3' : 'min-w-0 flex-1'}>
        <span className="mr-1.5">{label}</span>
        {chips}
      </span>
      <Amount value={amount} asset={asset} signed={signed} className="text-right" />
      <span className="w-9 shrink-0 text-right text-[11px] text-muted-foreground tabular-nums">
        {count !== undefined ? `(${count})` : ''}
      </span>
      <span className="w-4 shrink-0">{check !== undefined && <CheckMark ok={check} />}</span>
    </div>
  );
}

function LineChips({ line }: { line: StatementLine }) {
  return (
    <span className="inline-flex flex-wrap gap-1 align-middle">
      <OutcomeBadge outcome={line.outcome} />
      {line.earlierDay && <span className="text-[11px] text-muted-foreground">entered on an earlier day</span>}
    </span>
  );
}

function StatementCard({ asset, statement: s, previousDay }: { asset: string; statement: AssetStatement; previousDay?: string }) {
  const zero = (v: string) => BigInt(v) === 0n;
  return (
    <section>
      <SectionTitle title={`Statement · ${assetCode(asset)}`} hint={`Amounts in ${asset}, psp − product.`} />
      <Card className="grid gap-6 p-4 text-sm md:grid-cols-2">
        <div className="min-w-0">
          <h4 className={EYEBROW}>Bridge</h4>
          <Row label="PSP — finalised payments in the window" amount={s.psp.amount} count={s.psp.count} asset={asset} />
          <Row op="−" label="Product — applications in the window" amount={s.product.amount} count={s.product.count} asset={asset} />
          <Row op="=" label="Net difference" amount={s.net} asset={asset} signed strong />
          <p className="mt-1 pl-5 text-[11px] text-muted-foreground">explained by</p>
          {s.lines.map((line, i) => (
            <Row
              key={i}
              indent
              label={words(line.class)}
              chips={<LineChips line={line} />}
              amount={line.amount}
              count={line.count}
              asset={asset}
              signed
            />
          ))}
          <Row op="=" label="Unexplained residual" amount={s.residual} asset={asset} check={zero(s.residual)} strong />
          {s.carriedOutside.length > 0 && (
            <>
              <p className="mt-3 text-[11px] text-muted-foreground">Carried from earlier days, outside the window&apos;s net</p>
              {s.carriedOutside.map((line, i) => (
                <Row
                  key={i}
                  indent
                  label={words(line.class)}
                  chips={<OutcomeBadge outcome={line.outcome} />}
                  amount={line.amount}
                  count={line.count}
                  asset={asset}
                />
              ))}
            </>
          )}
          <div className="mt-3 border-t pt-2">
            <Row label="Gross open flow breaks, Σ|drift|" amount={s.flowGross} asset={asset} />
          </div>
        </div>

        <div className="min-w-0 space-y-6">
          <div>
            <h4 className={EYEBROW}>Open items</h4>
            <Row
              label={previousDay ? `At the previous cut (${formatDay(previousDay)})` : 'Seeded at the previous cut'}
              amount={s.suspense.openPrev}
              count={s.suspense.countPrev}
              asset={asset}
            />
            <Row op="+" label="Net difference of the window" amount={s.net} asset={asset} signed />
            <Row op="±" label="Entered through a lookup" amount={s.suspense.fromLookups} asset={asset} signed />
            <Row
              op="="
              label="At this cut, handed to the next run"
              amount={s.suspense.open}
              count={s.suspense.count}
              asset={asset}
              check={s.suspense.continuityOk}
              strong
            />
          </div>

          <div>
            <h4 className={EYEBROW}>Unclassified</h4>
            {(s.unclassified ?? []).length === 0 ? (
              <p className="text-xs text-muted-foreground">None.</p>
            ) : (
              (s.unclassified ?? []).map((u, i) => (
                <div key={i} className="flex items-start gap-2 py-1 text-xs">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-foreground" />
                  <span className="min-w-0 flex-1">
                    {u.count} {u.side.toUpperCase()} transaction{u.count === 1 ? '' : 's'} with state{' '}
                    <span className="font-mono">{u.state}</span>: the rule&apos;s state sets or the connector mapping need
                    attention.
                  </span>
                  <Amount value={u.amount} asset={asset} />
                </div>
              ))
            )}
          </div>
        </div>
      </Card>
    </section>
  );
}

// ── Books ────────────────────────────────────────────────────────────────────

const BUCKETS = ['0-1d', '2-7d', '8-30d', '>30d'];

function BooksSection({ books, wrongSign }: { books: Book[]; wrongSign: NonNullable<RunResults['wrongSign']> }) {
  const letteredOutside = books.filter((b) => BigInt(b.letteredOther) !== 0n);
  return (
    <section>
      <SectionTitle title="Open books at the cut" hint="Per side, hold prefix and asset, in the open direction: open = prev + opened − lettered." />
      <DataTable minWidth="46rem">
        <THead>
          <Th>Book</Th>
          <Th right>Prev</Th>
          <Th right>Opened</Th>
          <Th right>Lettered</Th>
          <Th right>Open</Th>
          <Th right>Holds</Th>
          {BUCKETS.map((b) => (
            <Th key={b} right>
              {b}
            </Th>
          ))}
          <Th right>Continuity</Th>
        </THead>
        <tbody className="divide-y">
          {books.map((b) => {
            const wrong = wrongSign.find((w) => w.side === b.side && w.prefix === b.prefix && w.asset === b.asset)?.holds ?? 0;
            return (
              <tr key={`${b.side}:${b.prefix}:${b.asset}`}>
                <Td className="min-w-52">
                  <div>
                    <span className="mr-1.5 text-[10px] font-medium uppercase text-muted-foreground">{b.side}</span>
                    <span className="font-mono break-all">{b.prefix}</span>
                  </div>
                  <div className="text-[10px] text-muted-foreground">
                    {b.asset} · opens {b.openSign}
                    {wrong > 0 && <span className="ml-1.5 text-destructive-foreground">{wrong} wrong sign</span>}
                  </div>
                </Td>
                <Td right><Amount value={b.openPrev} asset={b.asset} /></Td>
                <Td right><Amount value={b.opened} asset={b.asset} /></Td>
                <Td right>
                  <Amount value={b.lettered} asset={b.asset} />
                  {BigInt(b.letteredOther) !== 0n && (
                    <div className="text-[10px] text-muted-foreground">
                      other <Amount value={b.letteredOther} asset={b.asset} />
                    </div>
                  )}
                </Td>
                <Td right className="font-medium"><Amount value={b.open} asset={b.asset} /></Td>
                <Td right className="tabular-nums">{b.count}</Td>
                {BUCKETS.map((k) => (
                  <Td key={k} right className="tabular-nums text-muted-foreground">
                    {b.buckets?.[k] ? <span className="text-foreground">{b.buckets[k]}</span> : '—'}
                  </Td>
                ))}
                <Td right><CheckMark ok={b.continuityOk} /></Td>
              </tr>
            );
          })}
        </tbody>
      </DataTable>
      <p className="mt-2 text-xs text-muted-foreground">
        Lettered outside matching:{' '}
        {letteredOutside.length === 0
          ? 'none.'
          : letteredOutside.map((b, i) => (
              <Fragment key={i}>
                {i > 0 && ', '}
                <span className="font-mono">{b.prefix}</span> <Amount value={b.letteredOther} asset={b.asset} />
              </Fragment>
            ))}
      </p>
    </section>
  );
}

// ── Payment accounts ─────────────────────────────────────────────────────────

function Residual({ value, asset }: { value: string; asset: string }) {
  const zero = BigInt(value) === 0n;
  return (
    <span className="inline-flex items-center gap-1">
      <Amount value={value} asset={asset} signed className={zero ? undefined : 'text-destructive-foreground'} />
      <CheckMark ok={zero} />
    </span>
  );
}

function PaymentAccountsSection({ accounts }: { accounts: PaymentAccount[] }) {
  return (
    <section>
      <SectionTitle
        title="Payment-account book"
        hint="Did the PSP payment account move only as the flow says? Residual = the account's movement since the previous cut − the flow's."
      />
      {accounts.length === 0 ? (
        <EmptyNote>No payment account.</EmptyNote>
      ) : (
        <DataTable minWidth="52rem">
          <THead>
            <Th>Account</Th>
            <Th right>Input prev → cut</Th>
            <Th right>Flow credits</Th>
            <Th right>Credit residual</Th>
            <Th right>Output prev → cut</Th>
            <Th right>Flow debits</Th>
            <Th right>Debit residual</Th>
          </THead>
          <tbody className="divide-y">
            {accounts.map((a) => (
              <tr key={`${a.account}:${a.asset}`}>
                <Td className="min-w-48">
                  <div className="font-mono break-all">{a.account}</div>
                  <div className="text-[10px] text-muted-foreground">{a.asset}</div>
                </Td>
                <Td right>
                  <Amount value={a.inputPrev} asset={a.asset} className="text-muted-foreground" /> →{' '}
                  <Amount value={a.input} asset={a.asset} />
                </Td>
                <Td right><Amount value={a.flowCredits} asset={a.asset} /></Td>
                <Td right><Residual value={a.creditResidual} asset={a.asset} /></Td>
                <Td right>
                  <Amount value={a.outputPrev} asset={a.asset} className="text-muted-foreground" /> →{' '}
                  <Amount value={a.output} asset={a.asset} />
                </Td>
                <Td right><Amount value={a.flowDebits} asset={a.asset} /></Td>
                <Td right><Residual value={a.debitResidual} asset={a.asset} /></Td>
              </tr>
            ))}
          </tbody>
        </DataTable>
      )}
    </section>
  );
}
