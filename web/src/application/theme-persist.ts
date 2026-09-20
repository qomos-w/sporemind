import type { ThemeState } from '@qomos/sporemind-theme'
import { defaultTheme, parseThemeJSON } from '@qomos/sporemind-theme'
import { client, waitForClientReady, AUTH_CONNECTION_TIMEOUT_MS } from './generated-client'
import * as workspace_preferences from '../gen-clients/workspace/client'
import type { SaveAccountPreferencesCommand } from '../gen-types/workspace'

let writeVersion = 0

function themeKey(scope: string): string {
  return `theme.${scope}.v1`
}

// The backend replaces the entire preferences map on every save, so two
// overlapping read-modify-write cycles silently drop one key (lost-update:
// file-mode favorites vanished whenever the debounced path save overlapped a
// favorite toggle). All saves go through this single chain, one at a time.
let preferenceWriteQueue: Promise<void> = Promise.resolve()

export function savePreference(
  key: string,
  value: string,
  requestIdBase: string,
  version: { v: number },
): Promise<void> {
  const run = preferenceWriteQueue.then(async () => {
    // A failed snapshot read aborts the save: proceeding with an empty map
    // would wipe every other preference key.
    const existing = await workspace_preferences.preferencesGet(client)
    const preferences = { ...(existing?.Preferences ?? {}), [key]: value }
    const command: SaveAccountPreferencesCommand = {
      RequestId: `${requestIdBase}-${++version.v}`,
      AccountId: existing?.AccountId ?? 'root',
      Version: existing?.Version ?? 1,
      Preferences: preferences,
    }
    await workspace_preferences.preferencesSave(client, command)
  })
  // Keep the chain alive for later writers; the caller still observes the
  // rejection through `run`.
  preferenceWriteQueue = run.catch(() => {})
  return run
}

export async function loadPreference(key: string): Promise<string | undefined> {
  try {
    await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
    const snapshot = await workspace_preferences.preferencesGet(client)
    return snapshot.Preferences?.[key]
  } catch {
    return undefined
  }
}

export async function persistTheme(scope: string, state: ThemeState): Promise<void> {
  await savePreference(themeKey(scope), JSON.stringify(state), `theme-${scope}`, { v: writeVersion })
}

export async function loadTheme(scope: string): Promise<ThemeState> {
  try {
    await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
    const snapshot = await workspace_preferences.preferencesGet(client)
    return parseThemeJSON(snapshot.Preferences?.[themeKey(scope)])
  } catch {
    return defaultTheme
  }
}

// Synchronous initial theme from the boot script injected by the desktop
// shell (web/public/__theme.js + the Wails asset middleware). Keeps the very
// first paint on the persisted palette while loadTheme is still in flight.
export function initialTheme(): ThemeState {
  const injected = (window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__
  return typeof injected === 'string' ? parseThemeJSON(injected) : defaultTheme
}
