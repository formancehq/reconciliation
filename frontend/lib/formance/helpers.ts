/**
 * Standalone-app equivalents of the console helpers the reconcile UI expects.
 *
 * In the console these read the Formance device-flow session. Here the app is a
 * plain client of the recon HTTP API, so the "connected user" is just a locally
 * remembered actor used to prefill the `by` field on alert actions.
 */

const ACTOR_KEY = 'recon.actor';

/** The email used to prefill alert-action actors; empty when unset. */
export function getConnectedUserEmail(): string {
  if (typeof window === 'undefined') return '';
  try {
    return window.localStorage.getItem(ACTOR_KEY) || '';
  } catch {
    return '';
  }
}

/** Remember the actor email locally (used by alert ack/resolve/accept forms). */
export function setConnectedUserEmail(email: string): void {
  if (typeof window === 'undefined') return;
  try {
    if (email) window.localStorage.setItem(ACTOR_KEY, email);
    else window.localStorage.removeItem(ACTOR_KEY);
  } catch {
    /* ignore persistence failures */
  }
}
