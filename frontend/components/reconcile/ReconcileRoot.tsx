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
import { LayoutDashboard, ListChecks, Bell, LineChart, ShieldCheck, AlertTriangle } from 'lucide-react';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { TABBAR_ROW, TABBAR_LIST, TABBAR_TRIGGER, TABBAR_ICON } from '@/lib/uiClasses';
import { ReconProvider, useReconNav, type ReconTab } from './ReconContext';
import { HealthPill } from './ui';
import { EndpointSwitcher } from './EndpointSwitcher';
import { OverviewPanel } from './panels/OverviewPanel';
import { RulesPanel } from './panels/RulesPanel';
import { AlertsPanel } from './panels/AlertsPanel';
import { InsightsPanel } from './panels/InsightsPanel';
import { AuditPanel } from './panels/AuditPanel';

const TABS: { id: ReconTab; label: string; icon: typeof LayoutDashboard }[] = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard },
  { id: 'rules', label: 'Rules', icon: ListChecks },
  { id: 'alerts', label: 'Alerts', icon: Bell },
  { id: 'insights', label: 'Insights', icon: LineChart },
  { id: 'audit', label: 'Audit', icon: ShieldCheck },
];

function ReconcileInner() {
  const { nav, setTab, health, recheckHealth } = useReconNav();

  return (
    <div className="flex h-full flex-col">
      {/* Tab bar + health pill (Explore-Ledger tab style). */}
      <div className={`${TABBAR_ROW} flex flex-wrap items-center gap-2`}>
        <Tabs value={nav.tab} onValueChange={(v) => setTab(v as ReconTab)} className="min-w-0 flex-1">
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
        {nav.tab === 'overview' && <OverviewPanel />}
        {nav.tab === 'rules' && <RulesPanel />}
        {nav.tab === 'alerts' && <AlertsPanel />}
        {nav.tab === 'insights' && <InsightsPanel />}
        {nav.tab === 'audit' && <AuditPanel />}
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
