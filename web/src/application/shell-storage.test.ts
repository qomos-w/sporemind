import { describe, expect, it, vi, beforeEach } from 'vitest'
import { shellStorageAdapter } from './shell-storage'
import type { WorkspaceUIModel } from '../gen-types/workspace'

const emptyModel: WorkspaceUIModel = {
  WorkspaceId: 'default',
  Version: 1,
  SchemaVersion: 1,
  Layout: { Version: 1, LayoutJson: '', ActiveViewId: '' },
  Panels: { Version: 2, Panels: {}, ZoneTabOrder: {} },
  Dock: { LeftWidth: 260, RightWidth: 260, BottomHeight: 260, LeftTopPct: 0.5, RightTopPct: 0.5, BottomLeftPct: 0.5, WindowW: 1400, WindowH: 900 },
  AiShell: { SidebarOrder: [], SidebarPinned: [], LayoutJson: '' },
  ProjectCardBrowser: { SortMode: 'name-asc' },
  Explorer: { ActiveProjectId: '', SelectedPath: '', ExpandedPaths: [] },
}

let model = { ...emptyModel }

vi.mock('./workspace-ui-client', () => ({
  loadWorkspaceUIModel: vi.fn(async () => model),
  saveWorkspaceUIModel: vi.fn(async (_kind: string, buildPayload: (m: WorkspaceUIModel) => unknown) => {
    const payload = buildPayload(model) as { Panels?: typeof model.Panels; Dock?: typeof model.Dock; Layout?: typeof model.Layout }
    if (payload.Panels) model = { ...model, Panels: payload.Panels }
    if (payload.Dock) model = { ...model, Dock: payload.Dock }
    if (payload.Layout) model = { ...model, Layout: payload.Layout }
    return model
  }),
}))

describe('shell-storage panels round-trip', () => {
  beforeEach(() => {
    model = { ...emptyModel }
  })

  it('saves and restores panel visible state', async () => {
    const panelsJson = JSON.stringify({
      version: 2,
      panels: {
        explorer: {
          mode: 'pinned',
          visible: true,
          pos: { x: 0, y: 0 },
          size: { w: 320, h: 400 },
          zIndex: 1,
          dockZone: 'left-top',
        },
      },
      zoneTabOrder: { 'left-top': ['explorer'] },
    })

    await shellStorageAdapter.set('sporemind-panels-v1', panelsJson)

    const restored = await shellStorageAdapter.get('sporemind-panels-v1')
    expect(restored).not.toBeNull()
    const parsed = JSON.parse(restored!)
    expect(parsed.panels.explorer.visible).toBe(true)
    expect(parsed.panels.explorer.dockZone).toBe('left-top')
  })
})
