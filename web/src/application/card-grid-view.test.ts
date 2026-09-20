import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./theme-persist', () => ({
  loadPreference: vi.fn(),
  savePreference: vi.fn(() => Promise.resolve()),
}))

import { loadPreference, savePreference } from './theme-persist'

describe('card-grid-view', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.clearAllMocks()
  })

  it('defaults to flat and ignores unknown persisted values', async () => {
    vi.mocked(loadPreference).mockResolvedValue('bogus')
    const mod = await import('./card-grid-view')
    await mod.ensureCardGridViewLoaded()
    expect(mod.getCardGridView()).toBe('flat')
  })

  it('restores the persisted split view on first load', async () => {
    vi.mocked(loadPreference).mockResolvedValue('split')
    const mod = await import('./card-grid-view')
    await mod.ensureCardGridViewLoaded()
    expect(mod.getCardGridView()).toBe('split')
    expect(loadPreference).toHaveBeenCalledWith('ui.cardGridView.v1')
  })

  it('loads preferences only once', async () => {
    vi.mocked(loadPreference).mockResolvedValue('flat')
    const mod = await import('./card-grid-view')
    await mod.ensureCardGridViewLoaded()
    await mod.ensureCardGridViewLoaded()
    expect(loadPreference).toHaveBeenCalledTimes(1)
  })

  it('persists changes through workspace preferences', async () => {
    const mod = await import('./card-grid-view')
    mod.setCardGridView('split')
    expect(mod.getCardGridView()).toBe('split')
    expect(savePreference).toHaveBeenCalledWith(
      'ui.cardGridView.v1',
      'split',
      'card-grid-view',
      expect.any(Object),
    )
  })

  it('does not write when the view is unchanged', async () => {
    const mod = await import('./card-grid-view')
    mod.setCardGridView('flat')
    expect(savePreference).not.toHaveBeenCalled()
  })

  it('swallows persistence failures', async () => {
    vi.mocked(savePreference).mockRejectedValueOnce(new Error('offline'))
    const mod = await import('./card-grid-view')
    mod.setCardGridView('split')
    expect(mod.getCardGridView()).toBe('split')
    // Give the rejected promise a chance to surface an unhandled rejection.
    await new Promise(resolve => setTimeout(resolve, 0))
  })
})
