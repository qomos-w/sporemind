import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./theme-persist', () => ({
  savePreference: vi.fn().mockResolvedValue(undefined),
  loadPreference: vi.fn().mockResolvedValue(undefined),
}))

import {
  loadSshSessionUIPrefs,
  saveSshSessionUIPrefs,
  DEFAULT_SSH_SESSION_UI_PREFS,
} from './ssh-ui-state'
import { loadPreference, savePreference } from './theme-persist'
import { FILE_COLUMN_MIN } from '../ui/views/sshFileColumns'

const mockedLoad = vi.mocked(loadPreference)
const mockedSave = vi.mocked(savePreference)

beforeEach(() => {
  vi.clearAllMocks()
  mockedLoad.mockResolvedValue(undefined)
})

describe('loadSshSessionUIPrefs', () => {
  it('returns null when nothing is saved', async () => {
    mockedLoad.mockResolvedValue(undefined)
    await expect(loadSshSessionUIPrefs()).resolves.toBeNull()
  })

  it('round-trips a saved patch merged onto defaults', async () => {
    await saveSshSessionUIPrefs({ fileViewMode: 'list', statusWidth: 320 })
    const raw = mockedSave.mock.calls[0]![1]
    mockedLoad.mockResolvedValue(raw)

    const prefs = await loadSshSessionUIPrefs()
    expect(prefs).not.toBeNull()
    expect(prefs!.fileViewMode).toBe('list')
    expect(prefs!.statusWidth).toBe(320)
    expect(prefs!.filesHeight).toBe(DEFAULT_SSH_SESSION_UI_PREFS.filesHeight)
    expect(prefs!.treeSidebarOpen).toBe(true)
    expect(prefs!.bottomTab).toBe('ftp')
  })

  it('merges a second patch without losing the first', async () => {
    await saveSshSessionUIPrefs({ statusWidth: 300 })
    // Simulate the stored blob from the first save for the second read.
    const firstBlob = mockedSave.mock.calls[0]![1]
    mockedLoad.mockResolvedValueOnce(firstBlob)
    await saveSshSessionUIPrefs({ commandTreeWidth: 240 })
    const secondBlob = mockedSave.mock.calls[1]![1]
    mockedLoad.mockResolvedValueOnce(secondBlob)

    const prefs = await loadSshSessionUIPrefs()
    expect(prefs!.statusWidth).toBe(300)
    expect(prefs!.commandTreeWidth).toBe(240)
  })

  it('clamps out-of-range numbers to safe bounds', async () => {
    mockedLoad.mockResolvedValue(JSON.stringify({
      statusWidth: -5,
      filesHeight: 999999999,
      filesTreeWidth: 'not-a-number',
      commandTreeWidth: 200,
    }))
    const prefs = await loadSshSessionUIPrefs()
    expect(prefs!.statusWidth).toBe(160)
    expect(prefs!.filesHeight).toBe(8000)
    expect(prefs!.filesTreeWidth).toBe(DEFAULT_SSH_SESSION_UI_PREFS.filesTreeWidth)
    expect(prefs!.commandTreeWidth).toBe(200)
  })

  it('clamps column widths to their hard floors', async () => {
    mockedLoad.mockResolvedValue(JSON.stringify({
      colWidths: { name: 10, size: 60, mode: 'x', modified: 200 },
    }))
    const prefs = await loadSshSessionUIPrefs()
    expect(prefs!.colWidths.name).toBe(FILE_COLUMN_MIN.name)
    expect(prefs!.colWidths.size).toBe(60)
    // non-number falls back to the default, not the floor
    expect(prefs!.colWidths.mode).toBe(DEFAULT_SSH_SESSION_UI_PREFS.colWidths.mode)
    expect(prefs!.colWidths.modified).toBe(200)
  })

  it('rejects corrupt JSON by returning null', async () => {
    mockedLoad.mockResolvedValue('not json {{{')
    await expect(loadSshSessionUIPrefs()).resolves.toBeNull()
  })

  it('coerces unknown enum-ish values to defaults', async () => {
    mockedLoad.mockResolvedValue(JSON.stringify({
      fileViewMode: 'grid',
      bottomTab: 'history',
      statusCollapsed: 'yes',
    }))
    const prefs = await loadSshSessionUIPrefs()
    expect(prefs!.fileViewMode).toBe('icons')
    expect(prefs!.bottomTab).toBe('ftp')
    expect(prefs!.statusCollapsed).toBe(false)
  })
})
