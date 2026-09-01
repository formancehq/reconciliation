'use client';

/**
 * Minimal standalone-app equivalent of the console's feature-selection store.
 *
 * In the console this gated which modules were enabled. In the standalone app
 * reconciliation is the whole app, so `enabledIds` is undefined — meaning
 * "everything enabled" (see lib/recon/useReconAvailability, which treats an
 * undefined set as enabled).
 */
import { create } from 'zustand';

interface FeatureStore {
  /** Undefined = all features enabled (standalone reconciliation app). */
  enabledIds: Set<string> | undefined;
}

export const useFeatureStore = create<FeatureStore>(() => ({
  enabledIds: undefined,
}));
