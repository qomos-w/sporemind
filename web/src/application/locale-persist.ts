import { client, waitForClientReady, AUTH_CONNECTION_TIMEOUT_MS } from './generated-client'
import * as workspace_preferences from '../gen-clients/workspace/client'
import type { SaveAccountPreferencesCommand } from '../gen-types/workspace'
import type { Locale } from '../i18n'
import { isLocale } from '../i18n'

const LOCALE_KEY = 'locale'
let writeVersion = 0

export async function loadLocale(): Promise<Locale | null> {
  try {
    await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
    const snapshot = await workspace_preferences.preferencesGet(client)
    const raw = snapshot.Preferences?.[LOCALE_KEY]
    if (isLocale(raw)) {
      return raw
    }
  } catch {
    // ignore
  }
  return null
}

export async function persistLocale(locale: Locale): Promise<void> {
  const existing = await workspace_preferences.preferencesGet(client).catch(() => null)
  const preferences = { ...(existing?.Preferences ?? {}), [LOCALE_KEY]: locale }
  const command: SaveAccountPreferencesCommand = {
    RequestId: `locale-${++writeVersion}`,
    AccountId: existing?.AccountId ?? 'root',
    Version: existing?.Version ?? 1,
    Preferences: preferences,
  }
  await workspace_preferences.preferencesSave(client, command)
}
