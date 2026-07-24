'use client';

/**
 * Shared reconciliation-backend availability probe.
 *
 * The Reconcile entry points (sidebar button, User Guide docs) must appear only
 * when the recon backend (RECON_API_URL, default localhost:8081) is actually
 * reachable. That decision is needed BEFORE the Reconcile tab — and its
 * ReconContext health probe — ever mounts, so this lives in a small standalone
 * store that any component can read.
 *
 * A single store and monitor mean the sidebar and the guide share one bounded
 * discovery burst. The status never passes through an intermediate "checking"
 * value, so a re-probe never flickers the button off; it only flips once a
 * fresh result lands.
 */

import { useEffect } from 'react';
import { create } from 'zustand';
import { useFeatureStore } from '@/stores/featureStore';
import { reconClient } from './client';
import createLogger from '@/lib/logger';

const log = createLogger('ReconAvailability');

export type ReconStatus = 'unknown' | 'up' | 'down';

let loggedDown = false; // one-time log when the backend is first seen as down
let inFlightProbe: Promise<void> | undefined;

/** localStorage flag: force the Reconcile tab visible even when the probe is down. */
const FORCE_KEY = 'recon.force';
const loadForce = (): boolean => {
  try { return typeof window !== 'undefined' && window.localStorage.getItem(FORCE_KEY) === '1'; }
  catch { return false; }
};

interface ReconAvailabilityStore {
  status: ReconStatus;
  /** In-flight guard so concurrent callers share one request. */
  probing: boolean;
  /**
   * User override: keep the Reconcile entry points visible even when the health
   * probe reports the backend as down (e.g. the service is elsewhere, or will be
   * started later). The endpoint is configured separately (see recon endpoint).
   */
  force: boolean;
  probe: () => Promise<void>;
  setForce: (value: boolean) => void;
}

const useReconAvailabilityStore = create<ReconAvailabilityStore>((set) => ({
  status: 'unknown',
  probing: false,
  force: loadForce(),
  probe: () => {
    if (inFlightProbe) return inFlightProbe;
    set({ probing: true });
    inFlightProbe = (async () => {
      try {
        const ok = await reconClient.health();
        set({ status: ok ? 'up' : 'down' });
        if (ok) loggedDown = false;
        else if (!loggedDown) { loggedDown = true; log.debug('recon backend unreachable — hiding Reconcile entry points (unless forced)'); }
      } catch {
        set({ status: 'down' });
      } finally {
        set({ probing: false });
        inFlightProbe = undefined;
      }
    })();
    return inFlightProbe;
  },
  setForce: (value) => {
    try {
      if (value) window.localStorage.setItem(FORCE_KEY, '1');
      else window.localStorage.removeItem(FORCE_KEY);
    } catch { /* ignore persistence failures (private mode etc.) */ }
    log.info('recon force-enable set', { value });
    set({ force: value });
  },
}));

export interface ReconAvailabilityMonitorOptions {
  check?: () => Promise<void>;
  /** Total automatic checks in the initial discovery burst. */
  automaticCheckCount?: number;
  /** Time from the first automatic check to the last. */
  automaticCheckWindowMs?: number;
}

/**
 * Coordinates Reconciliation discovery across every entry-point consumer.
 *
 * The first active consumer starts one bounded burst: an immediate check and,
 * by default, four more checks spread over the following minute. Once complete,
 * it remains idle. Manual checks are always available and never restart or
 * extend the automatic burst.
 */
export class ReconAvailabilityMonitor {
  private readonly check: () => Promise<void>;
  private readonly automaticCheckCount: number;
  private readonly automaticCheckIntervalMs: number;
  private activeConsumers = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private automaticBurstStarted = false;
  private automaticChecksRemaining = 0;
  private automaticCheckInProgress = false;

  constructor(options: ReconAvailabilityMonitorOptions = {}) {
    this.check = options.check ?? (() => useReconAvailabilityStore.getState().probe());
    this.automaticCheckCount = Math.max(1, options.automaticCheckCount ?? 5);
    const windowMs = Math.max(0, options.automaticCheckWindowMs ?? 60_000);
    this.automaticCheckIntervalMs = this.automaticCheckCount > 1
      ? windowMs / (this.automaticCheckCount - 1)
      : 0;
  }

  activate = (): (() => void) => {
    this.activeConsumers += 1;
    if (this.activeConsumers === 1) this.start();
    return () => {
      this.activeConsumers = Math.max(0, this.activeConsumers - 1);
      if (this.activeConsumers === 0) this.stop();
    };
  };

  refresh = (): Promise<void> => this.check();

  private start(): void {
    if (!this.automaticBurstStarted) {
      this.automaticBurstStarted = true;
      this.automaticChecksRemaining = this.automaticCheckCount;
    }
    if (
      this.automaticChecksRemaining > 0
      && !this.timer
      && !this.automaticCheckInProgress
    ) {
      void this.runAutomaticCheck();
    }
  }

  private stop(): void {
    clearTimeout(this.timer);
    this.timer = undefined;
  }

  private async runAutomaticCheck(): Promise<void> {
    if (
      this.activeConsumers === 0
      || this.automaticChecksRemaining <= 0
      || this.automaticCheckInProgress
    ) return;
    this.automaticCheckInProgress = true;
    this.automaticChecksRemaining -= 1;
    try {
      await this.refresh();
    } catch (error) {
      log.warn('Automatic reconciliation availability check failed', {
        error: error instanceof Error ? error.message : String(error),
      });
    } finally {
      this.automaticCheckInProgress = false;
      if (this.activeConsumers > 0 && this.automaticChecksRemaining > 0) {
        this.timer = setTimeout(() => {
          this.timer = undefined;
          void this.runAutomaticCheck();
        }, this.automaticCheckIntervalMs);
      }
    }
  }
}

const reconAvailabilityMonitor = new ReconAvailabilityMonitor();

/**
 * Reconcile service controls for a settings UI: live probe status, the manual
 * force override, and setters/re-probe. Lets the user point at a backend and
 * activate the tab from a place reachable even when the tab itself is hidden.
 */
export function useReconControls(): {
  status: ReconStatus;
  probing: boolean;
  force: boolean;
  setForce: (value: boolean) => void;
  probe: () => Promise<void>;
} {
  const status = useReconAvailabilityStore((s) => s.status);
  const probing = useReconAvailabilityStore((s) => s.probing);
  const force = useReconAvailabilityStore((s) => s.force);
  const setForce = useReconAvailabilityStore((s) => s.setForce);
  return {
    status,
    probing,
    force,
    setForce,
    probe: reconAvailabilityMonitor.refresh,
  };
}

/**
 * True when the Reconcile feature is enabled AND either its backend responds OR
 * the user forced it on. The first consumer starts five automatic checks spread
 * over one minute, then stops until the user explicitly checks again. Network
 * calls are skipped entirely when the feature is toggled off. The `force`
 * override lets the tab appear even when the probe is down, so the user can
 * reach it and point it at the right backend.
 */
export function useReconAvailable(): boolean {
  const enabledIds = useFeatureStore((s) => s.enabledIds);
  const enabled = !enabledIds || enabledIds.has('reconcile');
  const status = useReconAvailabilityStore((s) => s.status);
  const force = useReconAvailabilityStore((s) => s.force);

  useEffect(() => {
    if (!enabled) return;
    return reconAvailabilityMonitor.activate();
  }, [enabled]);

  return enabled && (status === 'up' || force);
}
