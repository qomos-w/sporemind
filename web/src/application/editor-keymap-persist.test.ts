import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./generated-client', () => ({
  client: {},
  waitForClientReady: vi.fn(() => Promise.resolve()),
  AUTH_CONNECTION_TIMEOUT_MS: 10_000,
}))

import * as workspace_preferences from '../gen-clients/workspace/client'
import type { KeymapStateV2 } from '../ui/editor/keymap-schemes'
import { loadEditorKeymap, persistEditorKeymap } from './editor-keymap-persist'

vi.mock('../gen-clients/workspace/client', () => ({
  preferencesGet: vi.fn(),
  preferencesSave: vi.fn(),
}))

describe('editor-keymap-persist', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads a v2 state with customs', async () => {
    vi.mocked(workspace_preferences.preferencesGet).mockResolvedValue({
      AccountId: 'root',
      Version: 1,
      Preferences: {
        'editor.keymap.v1': JSON.stringify({
          version: 2,
          selected: 'c1',
          customs: [{ id: 'c1', name: 'Mine', base: 'idea', overrides: { moveLineUp: 'Ctrl-Alt-u' } }],
        }),
      },
    } as any)

    const state = await loadEditorKeymap()
    expect(state.selected).toBe('c1')
    expect(state.customs[0]?.overrides.moveLineUp).toBe('Ctrl-Alt-u')
  })

  it('migrates v1 {preset} payloads to v2 on load', async () => {
    vi.mocked(workspace_preferences.preferencesGet).mockResolvedValue({
      AccountId: 'root',
      Version: 1,
      Preferences: { 'editor.keymap.v1': '{"preset":"idea"}' },
    } as any)

    const state = await loadEditorKeymap()
    expect(state).toEqual({ version: 2, selected: 'idea', customs: [] })
  })

  it('falls back to defaults when nothing is stored or JSON is broken', async () => {
    vi.mocked(workspace_preferences.preferencesGet).mockResolvedValue({
      AccountId: 'root',
      Version: 1,
      Preferences: { 'editor.keymap.v1': 'not-json' },
    } as any)

    const state = await loadEditorKeymap()
    expect(state).toEqual({ version: 2, selected: 'vscode', customs: [] })
  })

  it('persists the whole v2 state merged into existing preferences', async () => {
    vi.mocked(workspace_preferences.preferencesGet).mockResolvedValue({
      AccountId: 'root',
      Version: 2,
      Preferences: { locale: 'zh-CN' },
    } as any)
    vi.mocked(workspace_preferences.preferencesSave).mockResolvedValue({} as any)

    const state: KeymapStateV2 = {
      version: 2 as const,
      selected: 'c9',
      customs: [{ id: 'c9', name: 'Copy', base: 'idea', overrides: { undo: 'Mod-u' } }],
    }
    await persistEditorKeymap(state)

    expect(workspace_preferences.preferencesSave).toHaveBeenCalledTimes(1)
    const arg = vi.mocked(workspace_preferences.preferencesSave).mock.calls[0]![1]
    expect(arg.Preferences).toMatchObject({
      locale: 'zh-CN',
      'editor.keymap.v1': JSON.stringify(state),
    })
  })
})
