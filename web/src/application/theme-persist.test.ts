import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./generated-client', () => ({
  client: {},
  waitForClientReady: vi.fn(() => Promise.resolve()),
  AUTH_CONNECTION_TIMEOUT_MS: 10_000,
}))

import { waitForClientReady } from './generated-client'
import * as workspace_preferences from '../gen-clients/workspace/client'
import { initialTheme, loadTheme, persistTheme } from './theme-persist'

vi.mock('../gen-clients/workspace/client', () => ({
  preferencesGet: vi.fn(),
  preferencesSave: vi.fn(),
}))

describe('theme-persist', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    delete (window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__
  })

  it('initialTheme reads the injected boot theme', () => {
    ;(window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__ =
      '{"mode":"dark","fontSize":18}'
    expect(initialTheme()).toEqual({ mode: 'dark', fontSize: 18 })
  })

  it('initialTheme parses and clamps the light-mode hue tint', () => {
    ;(window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__ =
      '{"mode":"light","hue":210}'
    expect(initialTheme()).toEqual({ mode: 'light', fontSize: 16, hue: 210 })
    ;(window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__ =
      '{"mode":"light","hue":999}'
    expect(initialTheme()).toEqual({ mode: 'light', fontSize: 16, hue: 360 })
    ;(window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__ =
      '{"mode":"light","hue":-5}'
    expect(initialTheme()).toEqual({ mode: 'light', fontSize: 16, hue: 0 })
  })

  it('initialTheme drops malformed hue values', () => {
    ;(window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__ =
      '{"mode":"light","hue":"blue"}'
    expect(initialTheme()).toEqual({ mode: 'light', fontSize: 16 })
  })

  it('initialTheme falls back to default when the injected value is missing or malformed', () => {
    expect(initialTheme()).toEqual({ mode: 'light', fontSize: 16 })
    ;(window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__ = 42
    expect(initialTheme()).toEqual({ mode: 'light', fontSize: 16 })
    ;(window as unknown as { __SPOREMIND_THEME__?: unknown }).__SPOREMIND_THEME__ = '{not json'
    expect(initialTheme()).toEqual({ mode: 'light', fontSize: 16 })
  })

  it('loadTheme waits for the client to be ready before fetching preferences', async () => {
    vi.mocked(workspace_preferences.preferencesGet).mockResolvedValue({
      AccountId: 'root',
      Version: 2,
      Preferences: { 'theme.ai-shell.v1': '{"mode":"dark","fontSize":16}' },
    } as any)

    const theme = await loadTheme('ai-shell')

    expect(waitForClientReady).toHaveBeenCalledTimes(1)
    expect(workspace_preferences.preferencesGet).toHaveBeenCalledTimes(1)
    expect(theme).toEqual({ mode: 'dark', fontSize: 16 })
  })

  it('loadTheme falls back to defaultTheme when the client is not ready', async () => {
    vi.mocked(waitForClientReady).mockRejectedValueOnce(new Error('timeout'))

    const theme = await loadTheme('ai-shell')

    expect(waitForClientReady).toHaveBeenCalledTimes(1)
    expect(workspace_preferences.preferencesGet).not.toHaveBeenCalled()
    expect(theme).toEqual({ mode: 'light', fontSize: 16 })
  })

  it('persistTheme merges into existing preferences and saves', async () => {
    vi.mocked(workspace_preferences.preferencesGet).mockResolvedValue({
      AccountId: 'root',
      Version: 3,
      Preferences: { locale: 'zh-CN' },
    } as any)
    vi.mocked(workspace_preferences.preferencesSave).mockResolvedValue({} as any)

    await persistTheme('ai-shell', { mode: 'light', fontSize: 18 })

    expect(workspace_preferences.preferencesSave).toHaveBeenCalledTimes(1)
    const arg = vi.mocked(workspace_preferences.preferencesSave).mock.calls[0]![1]
    expect(arg.Preferences).toMatchObject({
      locale: 'zh-CN',
      'theme.ai-shell.v1': '{"mode":"light","fontSize":18}',
    })
  })
})
