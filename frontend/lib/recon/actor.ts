const ACTOR_KEY = "recon.operator"

interface ActorStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

function browserSessionStorage(): ActorStorage | null {
  if (typeof window === "undefined") return null
  try {
    return window.sessionStorage
  } catch {
    return null
  }
}

/** Resolve the audit actor without deriving identity from an unverified token. */
export function resolveReconActor(
  connectedUserEmail: string,
  storage: ActorStorage | null = browserSessionStorage()
): string {
  const authenticated = connectedUserEmail.trim()
  if (authenticated) return authenticated
  try {
    return storage?.getItem(ACTOR_KEY)?.trim() || ""
  } catch {
    return ""
  }
}

/** Remember a manually entered fallback for this tab only. */
export function rememberReconActor(
  actor: string,
  storage: ActorStorage | null = browserSessionStorage()
): void {
  if (!storage) return
  const normalized = actor.trim()
  try {
    if (normalized) storage.setItem(ACTOR_KEY, normalized)
    else storage.removeItem(ACTOR_KEY)
  } catch {
    /* ignore unavailable tab storage */
  }
}
