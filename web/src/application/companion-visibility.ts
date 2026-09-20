import { loadPreference, savePreference } from './theme-persist'

// Workspace account preference key for the toast overlay's user-chosen
// visibility. Persisted through the actor-owned preferences surface (no
// localStorage). Shared by the title-bar mute toggle (AIShellTopbar) and the
// overlay itself (ToastOverlay) so a restart respects the last toggle state.
const PREF_KEY = 'toast.companion.visible.v1'

let current = true
const listeners = new Set<() => void>()
let initPromise: Promise<void> | null = null

function notify(): void {
  for (const l of listeners) l()
}

/** Default is visible — toasts pop over the main UI as they arrive. */
export async function loadCompanionVisible(): Promise<boolean> {
  try {
    const raw = await loadPreference(PREF_KEY)
    return raw !== 'false'
  } catch {
    return true
  }
}

/** Hydrate the store from the persisted preference exactly once. */
export function ensureCompanionVisibleLoaded(): Promise<void> {
  initPromise ??= loadCompanionVisible().then((v) => {
    current = v
    notify()
  })
  return initPromise
}

/** useSyncExternalStore subscription. */
export function subscribeCompanionVisible(cb: () => void): () => void {
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
  }
}

/** useSyncExternalStore snapshot. */
export function getCompanionVisible(): boolean {
  return current
}

/** Flip visibility: update live listeners and persist through workspace prefs. */
export function setCompanionVisible(visible: boolean): void {
  current = visible
  notify()
  void saveCompanionVisible(visible).catch(() => {})
}

export async function saveCompanionVisible(visible: boolean): Promise<void> {
  await savePreference(PREF_KEY, visible ? 'true' : 'false', 'toast-companion', { v: Date.now() })
}
