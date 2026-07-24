'use client';

/**
 * Minimal standalone-app equivalent of the console's chart/navigation store.
 *
 * The reconcile UI only reads `selectedLedger` — the create-rule form uses it
 * as a best-effort default for a rule's target ledger. Here it defaults to
 * empty (fields stay free-text); the endpoint switcher / future controls can
 * set it.
 */
import { create } from 'zustand';

interface ChartStore {
  selectedLedger: string;
  setSelectedLedger: (ledger: string) => void;
}

export const useChartStore = create<ChartStore>((set) => ({
  selectedLedger: '',
  setSelectedLedger: (selectedLedger) => set({ selectedLedger }),
}));
