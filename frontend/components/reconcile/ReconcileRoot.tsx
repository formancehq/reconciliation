'use client';

/**
 * Reconcile ("Ledger Clarity") root surface.
 *
 * Rendered by the `reconcile` feature (registry) as a standalone view — its own
 * mini-app with a top-level tab bar (Overview / Rules / Alerts), independent of
 * the ledger connection and selected ledger. Talks to the recon backend through
 * the same-origin proxy (/api/recon → RECON_API_URL).
 *
 * Structurally mirrors the V3 Ledger Explorer (same DS tab-bar primitives) so
 * it feels native next to "Explore Ledger" in the sidebar.
 */
import { LayoutDashboard, ListChecks, Bell, LineChart, ShieldCheck, AlertTriangle, TableProperties } from 'lucide-react';
import dynamic from 'next/dynamic';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { TABBAR_ROW, TABBAR_LIST, TABBAR_TRIGGER, TABBAR_ICON } from '@/lib/uiClasses';
import { ReconProvider, useReconNav, type ReconTab } from './ReconContext';
import { HealthPill, Loading } from './ui';
import { EndpointSwitcher } from './EndpointSwitcher';
import { OverviewPanel } from './panels/OverviewPanel';
import { RulesPanel } from './panels/RulesPanel';
import { AlertsPanel } from './panels/AlertsPanel';
import { InsightsPanel } from './panels/InsightsPanel';
import { AuditPanel } from './panels/AuditPanel';
import { LETTERING_SOURCE_AVAILABLE } from '@/lib/lettering/source';

// The Results tab's code (DuckDB-WASM, the lettering SQL) loads when the tab opens, never with the page.
const ResultsPanel = dynamic(() => import('./panels/ResultsPanel').then((m) => m.ResultsPanel), {
  ssr: false,
  loading: () => <Loading label="Loading Results…" />,
});

const TABS: { id: ReconTab; label: string; icon: typeof LayoutDashboard }[] = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard },
  { id: 'rules', label: 'Rules', icon: ListChecks },
  { id: 'alerts', label: 'Alerts', icon: Bell },
  { id: 'insights', label: 'Insights', icon: LineChart },
  { id: 'audit', label: 'Audit', icon: ShieldCheck },
  // Lettering result files read with DuckDB-WASM: shown while a source exists (the dev server's test data).
  ...(LETTERING_SOURCE_AVAILABLE ? [{ id: 'results' as const, label: 'Results', icon: TableProperties }] : []),
];

function ReconcileInner() {
  const { nav, navReady, setTab, health, recheckHealth } = useReconNav();
  // No tab is selected or rendered until the hash is read, as on the server.
  const tab = navReady ? nav.tab : null;

  return (
    <div className="flex h-full flex-col">
      {/* Tab bar + health pill (Explore-Ledger tab style). */}
      <div className={`${TABBAR_ROW} flex flex-wrap items-center gap-2`}>
        <Tabs value={tab ?? ''} onValueChange={(v) => setTab(v as ReconTab)} className="min-w-0 flex-1">
          <TabsList className={TABBAR_LIST}>
            {TABS.map((t) => (
              <TabsTrigger key={t.id} value={t.id} className={TABBAR_TRIGGER}>
                <t.icon className={TABBAR_ICON} aria-hidden />
                <span>{t.label}</span>
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <div className="ml-auto flex shrink-0 items-center gap-1.5">
          <EndpointSwitcher />
          <HealthPill health={health} onRetry={recheckHealth} />
        </div>
      </div>

      {health === 'down' && (
        <div className="flex shrink-0 items-center gap-2 border-b bg-amber-background px-3 py-2 text-xs text-amber-foreground">
          <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
          <span>
            Can’t reach the reconciliation backend. Views will fail to load until it’s up
            (<code className="font-mono">go run . serve --ledger-insecure --listen :8081</code>).
          </span>
        </div>
      )}

      <div className="min-h-0 flex-1 overflow-auto">
        {tab === 'overview' && <OverviewPanel />}
        {tab === 'rules' && <RulesPanel />}
        {tab === 'alerts' && <AlertsPanel />}
        {tab === 'insights' && <InsightsPanel />}
        {tab === 'audit' && <AuditPanel />}
        {tab === 'results' && LETTERING_SOURCE_AVAILABLE && <ResultsPanel />}
      </div>
    </div>
  );
}

export default function ReconcileRoot() {
  return (
    <ReconProvider>
      <ReconcileInner />
    </ReconProvider>
  );
}
