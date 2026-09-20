import { describe, it, expect, vi, beforeEach } from 'vitest'

// In-memory backend mirroring workspace.handleGetAccountPreferences /
// handleSaveAccountPreferences: full-map replace, no merge.
let backendPrefs = {
  AccountId: 'root',
  Version: 1,
  Preferences: {} as Record<string, string>,
}
let failNextGet = false

vi.mock('./generated-client', () => ({
  client: {
    invoke: (callable: string, req: unknown) =>
      new Promise<unknown>((resolve, reject) => {
        setTimeout(() => {
          if (callable === 'workspace.preferences_get') {
            if (failNextGet) {
              failNextGet = false
              reject(new Error('preferences_get failed'))
              return
            }
            resolve(backendPrefs)
            return
          }
          if (callable === 'workspace.preferences_save') {
            const cmd = req as { AccountId: string; Version: number; Preferences: Record<string, string> }
            backendPrefs = {
              AccountId: cmd.AccountId,
              Version: cmd.Version,
              Preferences: { ...cmd.Preferences },
            }
            resolve(backendPrefs)
          }
        }, 10)
      }),
  },
  waitForClientReady: async () => {},
  AUTH_CONNECTION_TIMEOUT_MS: 1000,
}))

import { savePreference, loadPreference } from './theme-persist'

describe('savePreference', () => {
  beforeEach(() => {
    backendPrefs = { AccountId: 'root', Version: 1, Preferences: {} }
    failNextGet = false
  })

  it('round-trips a single key', async () => {
    await savePreference('fileBrowser.favorites.proj-1.v1', JSON.stringify([{ path: 'src', isDir: true }]), 'fb-fav-proj-1', { v: 0 })
    expect(await loadPreference('fileBrowser.favorites.proj-1.v1')).toBe(JSON.stringify([{ path: 'src', isDir: true }]))
  })

  it('serializes overlapping saves: both keys survive (lost-update regression)', async () => {
    // FileBrowser pattern: the debounced path save and a favorites toggle
    // overlap; without write serialization one key was silently dropped.
    await Promise.all([
      savePreference('fileBrowser.path.proj-1.v1', 'src', 'fb-path-proj-1', { v: 0 }),
      savePreference('fileBrowser.favorites.proj-1.v1', JSON.stringify([{ path: 'src', isDir: true }]), 'fb-fav-proj-1', { v: 0 }),
    ])
    expect(await loadPreference('fileBrowser.path.proj-1.v1')).toBe('src')
    expect(await loadPreference('fileBrowser.favorites.proj-1.v1')).toBe(JSON.stringify([{ path: 'src', isDir: true }]))
  })

  it('aborts the save when the snapshot read fails instead of wiping other keys', async () => {
    await savePreference('theme.ai-shell.v1', '{"mode":"dark"}', 'theme-ai-shell', { v: 0 })
    failNextGet = true
    await expect(savePreference('fileBrowser.favorites.proj-1.v1', '[]', 'fb-fav-proj-1', { v: 0 })).rejects.toThrow()
    expect(await loadPreference('theme.ai-shell.v1')).toBe('{"mode":"dark"}')
    // The queue stays usable after a failure.
    failNextGet = false
    await savePreference('fileBrowser.favorites.proj-1.v1', '[]', 'fb-fav-proj-1', { v: 0 })
    expect(await loadPreference('theme.ai-shell.v1')).toBe('{"mode":"dark"}')
    expect(await loadPreference('fileBrowser.favorites.proj-1.v1')).toBe('[]')
  })
})
