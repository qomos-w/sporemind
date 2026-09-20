// ── Dock size constants ──
export const DOCK_MIN_WIDTH = 160
export const DOCK_MIN_HEIGHT = 120
export const DOCK_DEFAULT_SIZE = 360

// ── Layout types for IDE-style split view system ──
// Layout types with ViewType made generic.

export type ViewType = string

export interface TabItem {
  id: string
  viewType: ViewType
  label: string
  closable: boolean
  metadata?: Record<string, string>
}

export type SplitDirection = 'horizontal' | 'vertical'

export interface SplitNode {
  type: 'split'
  id: string
  direction: SplitDirection  // horizontal = left|right, vertical = top|bottom
  ratio: number              // 0..1 split position
  first: SplitTreeNode
  second: SplitTreeNode
}

export interface LeafNode {
  type: 'leaf'
  id: string
  tabs: TabItem[]
  activeTabIndex: number
}

export type SplitTreeNode = SplitNode | LeafNode

export type DockZone =
  | 'left-top'
  | 'left-bottom'
  | 'right-top'
  | 'right-bottom'
  | 'bottom-left'
  | 'bottom-right'
  | null

export interface ToolbarItem {
  id: string
  type: 'separator' | 'custom'
  visible: boolean
  order: number
}

export interface SidePanelState {
  visible: boolean
  width: number
  activePanelId: string | null
}

export interface PersistedLayoutState {
  version: number
  splitTree: SplitTreeNode
  sidePanel: SidePanelState
  toolbarItems: ToolbarItem[]
}
