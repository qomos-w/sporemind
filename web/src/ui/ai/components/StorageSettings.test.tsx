import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { StorageSettingsSection } from './StorageSettings'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const appMock = vi.hoisted(() => ({
  GetStorageSettings: vi.fn(),
  SetStorageSettings: vi.fn(async () => {}),
  SaveConfig: vi.fn(async () => {}),
}))
vi.mock(
  '../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app',
  async (importOriginal) => ({
    ...((await importOriginal()) as Record<string, unknown>),
    ...appMock,
  })
)

vi.mock('../../../application/runtime', () => ({
  isWails: () => true,
  isCapacitor: () => false,
  isIframe: () => false,
  isWeb: () => false,
  getRuntime: () => ({ mode: 'wails' }),
  recomputeRuntime: () => ({ mode: 'wails' }),
}))

vi.mock('../../../application/generated-client', () => ({ client: {} }))

import {
  setStorageSettingsForTest,
} from '../../../application/storage-config'

const dom = document.createElement('div')

describe('StorageSettingsSection', () => {
  let root: Root

  beforeEach(() => {
    dom.innerHTML = ''
    root = createRoot(dom)
    appMock.GetStorageSettings.mockResolvedValue({
      backendAistats: '',
      backendLogs: 'fs',
      logsRetentionDays: 7,
      aistatsRawDays: 90,
      aistatsDailyDays: 550,
    })
    appMock.SetStorageSettings.mockClear()
    appMock.SaveConfig.mockClear()
    setStorageSettingsForTest(null)
  })

  it('loads settings from the backend and renders the backend rows', async () => {
    await act(async () => {
      root.render(<StorageSettingsSection />)
    })
    expect(appMock.GetStorageSettings).toHaveBeenCalled()
    // backendLogs=fs → the fs button carries aria-pressed=true
    const pressed = dom.querySelectorAll('[aria-pressed="true"]')
    expect(pressed.length).toBeGreaterThanOrEqual(2) // aistats default + logs fs
  })

  it('saves edited backends through SetStorageSettings + SaveConfig', async () => {
    await act(async () => {
      root.render(<StorageSettingsSection />)
    })
    const ldbButtons = Array.from(
      dom.querySelectorAll('button')
    ).filter((b) => b.textContent?.includes('optionLdb'))
    expect(ldbButtons.length).toBeGreaterThan(0)
    await act(async () => {
      ldbButtons[0]!.click() // aistats backend → explicit goleveldb
    })
    const saveBtn = Array.from(dom.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('settings.index.storage.save')
    )
    await act(async () => {
      saveBtn?.click()
    })
    await act(async () => {})
    expect(appMock.SetStorageSettings).toHaveBeenCalledWith(
      expect.objectContaining({ backendAistats: 'goleveldb', backendLogs: 'fs' })
    )
    expect(appMock.SaveConfig).toHaveBeenCalled()
  })

  it('rejects invalid backend values from the backend with an error', async () => {
    appMock.SetStorageSettings.mockRejectedValueOnce(
      new Error('backend_aistats: invalid backend "redis"')
    )
    await act(async () => {
      root.render(<StorageSettingsSection />)
    })
    const saveBtn = Array.from(dom.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('settings.index.storage.save')
    )
    await act(async () => {
      saveBtn?.click()
    })
    await act(async () => {})
    expect(dom.querySelector('[role="alert"]')?.textContent).toContain(
      'invalid backend'
    )
  })
})
