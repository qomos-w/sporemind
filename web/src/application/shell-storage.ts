import type { StorageAdapter, PanelState } from '@qomos/sporemind-shell'
import type {
  WorkspaceDockState,
  WorkspacePanelState,
  WorkspacePanelsState,
  WorkspaceShellLayout,
  XY,
  WH,
} from '../gen-types/workspace'
import { loadWorkspaceUIModel, saveWorkspaceUIModel } from './workspace-ui-client'

const LAYOUT_KEY = 'sporemind-layout-v1'
const PANELS_KEY = 'sporemind-panels-v1'
const DOCK_KEY = 'sporemind-dock-sizes-v1'
const LAYOUT_VERSION = 1
const PANELS_VERSION = 2
const DOCK_VERSION = 1

function toDockZone(value: string | null | undefined): PanelState['dockZone'] {
  return value == null || value === '' ? null : value as PanelState['dockZone']
}

function toNullableString(value: PanelState['dockZone']): string {
  return value ?? ''
}

function toXY(value: { x: number; y: number }): XY {
  return { X: value.x, Y: value.y }
}

function fromXY(value: XY | undefined): { x: number; y: number } {
  return { x: value?.X ?? 100, y: value?.Y ?? 80 }
}

function toWH(value: { w: number; h: number }): WH {
  return { W: value.w, H: value.h }
}

function fromWH(value: WH | undefined): { w: number; h: number } {
  return { w: value?.W ?? 300, h: value?.H ?? 420 }
}

function encodeLayout(value: string): WorkspaceShellLayout {
  return {
    Version: LAYOUT_VERSION,
    LayoutJson: value,
    ActiveViewId: '',
  }
}

async function getLayout(): Promise<string | null> {
  const model = await loadWorkspaceUIModel()
  const layout = model.Layout
  if (!layout?.LayoutJson) {
    console.debug('[shell-storage] layout restore: empty backend state')
    return null
  }
  console.debug('[shell-storage] layout restore: loaded backend state', {
    length: layout.LayoutJson.length,
    version: layout.Version ?? LAYOUT_VERSION,
  })
  return layout.LayoutJson
}

async function saveLayout(value: string | null): Promise<void> {
  console.debug('[shell-storage] layout save', { length: value?.length ?? 0 })
  await saveWorkspaceUIModel('Layout', () => ({
    Layout: encodeLayout(value ?? ''),
  }))
}

async function getPanels(): Promise<string | null> {
  const model = await loadWorkspaceUIModel()
  const panels = model.Panels
  if (!panels?.Panels) {
    console.debug('[shell-storage] panels restore: empty backend state')
    return null
  }
  const encoded: Record<string, PanelState> = {}
  for (const [id, panel] of Object.entries(panels.Panels)) {
    encoded[id] = {
      mode: panel.Mode as PanelState['mode'],
      visible: panel.Visible,
      pos: fromXY(panel.Pos),
      size: fromWH(panel.Size),
      zIndex: panel.ZIndex,
      dockZone: toDockZone(panel.DockZone),
    }
  }
  const json = JSON.stringify({
    version: panels.Version || PANELS_VERSION,
    panels: encoded,
    zoneTabOrder: panels.ZoneTabOrder ?? {},
  })
  console.debug('[shell-storage] panels restore: loaded backend state', {
    panelCount: Object.keys(encoded).length,
    zoneCount: Object.keys(panels.ZoneTabOrder ?? {}).length,
    length: json.length,
  })
  return json
}

async function savePanels(value: string | null): Promise<void> {
  await saveWorkspaceUIModel('Panels', () => ({
    Panels: encodePanels(value ?? ''),
  }))
}

async function getDock(): Promise<string | null> {
  const model = await loadWorkspaceUIModel()
  const dock = model.Dock
  if (!dock) {
    console.debug('[shell-storage] dock restore: empty backend state')
    return null
  }
  const json = JSON.stringify({
    version: DOCK_VERSION,
    leftWidth: dock.LeftWidth,
    rightWidth: dock.RightWidth,
    bottomHeight: dock.BottomHeight,
    leftTopPct: dock.LeftTopPct,
    rightTopPct: dock.RightTopPct,
    bottomLeftPct: dock.BottomLeftPct,
    windowW: dock.WindowW,
    windowH: dock.WindowH,
  })
  console.debug('[shell-storage] dock restore: loaded backend state', {
    length: json.length,
    leftWidth: dock.LeftWidth,
    rightWidth: dock.RightWidth,
    bottomHeight: dock.BottomHeight,
  })
  return json
}

async function saveDock(value: string | null): Promise<void> {
  console.debug('[shell-storage] dock save', { length: value?.length ?? 0 })
  await saveWorkspaceUIModel('Dock', () => ({
    Dock: encodeDock(value ?? ''),
  }))
}

function encodePanels(value: string): WorkspacePanelsState {
  const parsed = JSON.parse(value) as {
    version: number
    panels: Record<string, PanelState>
    zoneTabOrder?: Record<string, string[]>
  }
  const panels: Record<string, WorkspacePanelState> = {}
  for (const [id, panel] of Object.entries(parsed.panels ?? {})) {
    panels[id] = {
      Mode: panel.mode,
      Visible: panel.visible,
      Pos: toXY(panel.pos),
      Size: toWH(panel.size),
      ZIndex: panel.zIndex,
      DockZone: toNullableString(panel.dockZone),
    }
  }
  return {
    Version: parsed.version ?? PANELS_VERSION,
    Panels: panels,
    ZoneTabOrder: parsed.zoneTabOrder ?? {},
  }
}

function encodeDock(value: string): WorkspaceDockState {
  const parsed = JSON.parse(value) as {
    version: number
    leftWidth: number
    rightWidth: number
    bottomHeight: number
    leftTopPct: number
    rightTopPct: number
    bottomLeftPct: number
    windowW: number
    windowH: number
  }
  return {
    LeftWidth: Math.round(parsed.leftWidth),
    RightWidth: Math.round(parsed.rightWidth),
    BottomHeight: Math.round(parsed.bottomHeight),
    LeftTopPct: parsed.leftTopPct,
    RightTopPct: parsed.rightTopPct,
    BottomLeftPct: parsed.bottomLeftPct,
    WindowW: Math.round(parsed.windowW),
    WindowH: Math.round(parsed.windowH),
  }
}

export const shellStorageAdapter: StorageAdapter = {
  async get(key: string) {
    try {
      switch (key) {
        case LAYOUT_KEY:
          return await getLayout()
        case PANELS_KEY:
          return await getPanels()
        case DOCK_KEY:
          return await getDock()
        default:
          return null
      }
    } catch (error) {
      console.error('[shell-storage] restore failed', { key }, error)
      return null
    }
  },
  async set(key: string, value: string) {
    switch (key) {
      case LAYOUT_KEY:
        await saveLayout(value)
        return
      case PANELS_KEY:
        await savePanels(value)
        return
      case DOCK_KEY:
        await saveDock(value)
        return
      default:
        return
    }
  },
}
