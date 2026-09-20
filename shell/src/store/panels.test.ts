import { describe, expect, it } from 'vitest'
import { PanelManager, type PanelState } from './panels'

type StorageRecord = Record<string, string | null>

function createStorage(initial: StorageRecord = {}) {
  const data = new Map<string, string | null>(Object.entries(initial))
  return {
    async get(key: string) {
      return data.get(key) ?? null
    },
    async set(key: string, value: string) {
      data.set(key, value)
    },
  }
}

const defaultPanels: Record<string, PanelState> = {
  explorer: {
    mode: 'pinned',
    visible: true,
    pos: { x: 0, y: 0 },
    size: { w: 320, h: 400 },
    zIndex: 1,
    dockZone: 'left-top',
  },
  search: {
    mode: 'pinned',
    visible: false,
    pos: { x: 0, y: 0 },
    size: { w: 320, h: 400 },
    zIndex: 2,
    dockZone: 'left-top',
  },
  outline: {
    mode: 'pinned',
    visible: false,
    pos: { x: 0, y: 0 },
    size: { w: 320, h: 400 },
    zIndex: 3,
    dockZone: 'left-top',
  },
}

describe('PanelManager.init', () => {
  it('preserves persisted zone tab order and appends missing pinned panels', async () => {
    const storage = createStorage({
      'sporemind-panels-v1': JSON.stringify({
        version: 2,
        panels: {
          explorer: defaultPanels.explorer,
          search: defaultPanels.search,
          outline: defaultPanels.outline,
        },
        zoneTabOrder: {
          'left-top': ['search', 'explorer'],
        },
      }),
    })

    const manager = new PanelManager(storage, defaultPanels)
    await manager.init()

    expect(manager.getZonePanels('left-top')).toEqual(['search', 'explorer', 'outline'])
  })

  it('drops stale ids from persisted zone tab order while appending other pinned panels', async () => {
    const storage = createStorage({
      'sporemind-panels-v1': JSON.stringify({
        version: 2,
        panels: {
          explorer: defaultPanels.explorer,
          search: defaultPanels.search,
        },
        zoneTabOrder: {
          'left-top': ['stale-panel', 'search', 'explorer'],
        },
      }),
    })

    const manager = new PanelManager(storage, defaultPanels)
    await manager.init()

    expect(manager.getZonePanels('left-top')).toEqual(['search', 'explorer', 'outline'])
  })
})

describe('PanelManager visible round-trip', () => {
  it('restores an opened panel after activate + save + init', async () => {
    const hiddenDefaults: Record<string, PanelState> = {
      explorer: { ...defaultPanels.explorer, visible: false } as PanelState,
      search: { ...defaultPanels.search, visible: false } as PanelState,
      outline: { ...defaultPanels.outline, visible: false } as PanelState,
    }

    const storage = createStorage()
    const manager = new PanelManager(storage, hiddenDefaults)
    await manager.init()
    expect(manager.getActiveTabId('left-top')).toBeNull()

    manager.activateTab('explorer')
    expect(manager.getActiveTabId('left-top')).toBe('explorer')

    // Flush the debounced save
    await new Promise(r => setTimeout(r, 400))

    const saved = await storage.get('sporemind-panels-v1')
    expect(saved).not.toBeNull()
    const parsed = JSON.parse(saved!)
    expect(parsed.panels.explorer.visible).toBe(true)

    const restored = new PanelManager(storage, hiddenDefaults)
    await restored.init()

    expect(restored.getActiveTabId('left-top')).toBe('explorer')
  })
})
