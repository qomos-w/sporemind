// ── LayoutStore: manages split tree, toolbar, side panel with storage persistence ──

import type {
  SplitTreeNode,
  SplitNode,
  LeafNode,
  TabItem,
  SplitDirection,
  ToolbarItem,
  SidePanelState,
  PersistedLayoutState,
  ViewType,
} from '../types/layout'
import type { StorageAdapter } from '../adapters/storage'

const STORAGE_KEY = 'sporemind-layout-v1'
const LAYOUT_VERSION = 1
const SAVE_DEBOUNCE_MS = 300

type Listener = () => void

let nextId = 1
function genId(prefix: string): string {
  return `${prefix}-${nextId++}`
}

// ── Default state ──

function defaultSplitTree(): LeafNode {
  return {
    type: 'leaf',
    id: genId('leaf'),
    tabs: [
      { id: 'tab-home', viewType: 'home', label: 'Home', closable: false },
    ],
    activeTabIndex: 0,
  }
}

function defaultToolbarItems(): ToolbarItem[] {
  return []
}

function defaultSidePanel(): SidePanelState {
  return { visible: false, width: 300, activePanelId: null }
}

// ── Tree traversal helpers ──

function findNode(tree: SplitTreeNode, id: string): SplitTreeNode | null {
  if (tree.id === id) return tree
  if (tree.type === 'split') {
    return findNode(tree.first, id) || findNode(tree.second, id)
  }
  return null
}

function findParent(tree: SplitTreeNode, id: string): SplitNode | null {
  if (tree.type === 'split') {
    if (tree.first.id === id || tree.second.id === id) return tree
    return findParent(tree.first, id) || findParent(tree.second, id)
  }
  return null
}

/** Replace a node in the tree by id, returning new root */
function replaceNode(tree: SplitTreeNode, targetId: string, replacement: SplitTreeNode): SplitTreeNode {
  if (tree.id === targetId) return replacement
  if (tree.type === 'split') {
    return {
      ...tree,
      first: replaceNode(tree.first, targetId, replacement),
      second: replaceNode(tree.second, targetId, replacement),
    }
  }
  return tree
}

// ── LayoutStore ──

export class LayoutStore {
  splitTree: SplitTreeNode
  toolbarItems: ToolbarItem[]
  sidePanel: SidePanelState

  private listeners = new Set<Listener>()
  private _version = 0
  private saveTimer: ReturnType<typeof setTimeout> | null = null
  private storage: StorageAdapter

  constructor(storage: StorageAdapter) {
    this.storage = storage
    this.splitTree = defaultSplitTree()
    this.toolbarItems = defaultToolbarItems()
    this.sidePanel = defaultSidePanel()
  }

  /** Load persisted state from storage. Call once at app startup before first render. */
  async init(): Promise<void> {
    try {
      const raw = await this.storage.get(STORAGE_KEY)
      if (!raw) return
      const parsed: PersistedLayoutState = JSON.parse(raw)
      if (parsed.version !== LAYOUT_VERSION) return
      this.splitTree = parsed.splitTree
      this.toolbarItems = parsed.toolbarItems
      this.sidePanel = parsed.sidePanel
      this._version++
    } catch {
      // Corrupt or missing — keep defaults, no emit needed
    }
  }

  private scheduleSave() {
    if (this.saveTimer) clearTimeout(this.saveTimer)
    this.saveTimer = setTimeout(() => {
      this.saveTimer = null
      const state: PersistedLayoutState = {
        version: LAYOUT_VERSION,
        splitTree: this.splitTree,
        sidePanel: this.sidePanel,
        toolbarItems: this.toolbarItems,
      }
      this.storage.set(STORAGE_KEY, JSON.stringify(state)).catch(() => {})
    }, SAVE_DEBOUNCE_MS)
  }

  private emit() {
    this._version++
    for (const fn of this.listeners) fn()
    this.scheduleSave()
  }

  getVersion = () => this._version

  subscribe(fn: Listener): () => void {
    this.listeners.add(fn)
    return () => { this.listeners.delete(fn) }
  }

  // ── Split tree operations ──

  splitPane(
    leafId: string,
    direction: SplitDirection,
    newTab: TabItem,
    position: 'first' | 'second' = 'second',
  ) {
    const leaf = findNode(this.splitTree, leafId)
    if (!leaf || leaf.type !== 'leaf') return

    const newLeaf: LeafNode = {
      type: 'leaf',
      id: genId('leaf'),
      tabs: [newTab],
      activeTabIndex: 0,
    }

    const splitNode: SplitNode = {
      type: 'split',
      id: genId('split'),
      direction,
      ratio: 0.5,
      first: position === 'first' ? newLeaf : leaf,
      second: position === 'second' ? newLeaf : leaf,
    }

    this.splitTree = replaceNode(this.splitTree, leafId, splitNode)
    this.emit()
  }

  closePane(leafId: string) {
    const parent = findParent(this.splitTree, leafId)
    if (!parent) return // Root leaf — don't close

    const sibling = parent.first.id === leafId ? parent.second : parent.first
    this.splitTree = replaceNode(this.splitTree, parent.id, sibling)
    this.emit()
  }

  resizeSplit(splitId: string, ratio: number) {
    const node = findNode(this.splitTree, splitId)
    if (!node || node.type !== 'split') return
    ;(node as SplitNode).ratio = Math.max(0.1, Math.min(0.9, ratio))
    this.emit()
  }

  // ── Tab operations ──

  addTab(leafId: string, tab: TabItem) {
    const leaf = findNode(this.splitTree, leafId) as LeafNode | null
    if (!leaf || leaf.type !== 'leaf') return
    const existingIdx = leaf.tabs.findIndex(t => t.id === tab.id)
    if (existingIdx >= 0) {
      // Merge metadata (and any other scalar fields) so reopening an already-
      // open file with a new line number still propagates the new state to the
      // mounted editor.
      leaf.tabs[existingIdx] = { ...leaf.tabs[existingIdx], ...tab }
      leaf.activeTabIndex = existingIdx
    } else {
      leaf.tabs.push(tab)
      leaf.activeTabIndex = leaf.tabs.length - 1
    }
    this.emit()
  }

  removeTab(leafId: string, tabId: string) {
    const leaf = findNode(this.splitTree, leafId) as LeafNode | null
    if (!leaf || leaf.type !== 'leaf') return

    const idx = leaf.tabs.findIndex(t => t.id === tabId)
    if (idx < 0) return

    leaf.tabs.splice(idx, 1)

    if (leaf.tabs.length === 0) {
      this.closePane(leafId)
      return
    }

    if (leaf.activeTabIndex >= leaf.tabs.length) {
      leaf.activeTabIndex = leaf.tabs.length - 1
    }
    this.emit()
  }

  setActiveTab(leafId: string, index: number) {
    const leaf = findNode(this.splitTree, leafId) as LeafNode | null
    if (!leaf || leaf.type !== 'leaf') return
    if (index < 0 || index >= leaf.tabs.length) return
    leaf.activeTabIndex = index
    this.emit()
  }

  moveTab(fromLeafId: string, tabId: string, toLeafId: string, insertIndex?: number) {
    const fromLeaf = findNode(this.splitTree, fromLeafId) as LeafNode | null
    const toLeaf = findNode(this.splitTree, toLeafId) as LeafNode | null
    if (!fromLeaf || !toLeaf || fromLeaf.type !== 'leaf' || toLeaf.type !== 'leaf') return

    const tabIdx = fromLeaf.tabs.findIndex(t => t.id === tabId)
    if (tabIdx < 0) return

    const spliced = fromLeaf.tabs.splice(tabIdx, 1)
    const tab = spliced[0]!
    const idx = insertIndex !== undefined ? insertIndex : toLeaf.tabs.length
    toLeaf.tabs.splice(idx, 0, tab)
    toLeaf.activeTabIndex = idx

    if (fromLeaf.tabs.length === 0) {
      this.closePane(fromLeafId)
      return
    }

    if (fromLeaf.activeTabIndex >= fromLeaf.tabs.length) {
      fromLeaf.activeTabIndex = fromLeaf.tabs.length - 1
    }

    this.emit()
  }

  moveTabToEdge(
    fromLeafId: string,
    tabId: string,
    targetLeafId: string,
    edge: 'top' | 'bottom' | 'left' | 'right',
  ) {
    const fromLeaf = findNode(this.splitTree, fromLeafId) as LeafNode | null
    if (!fromLeaf || fromLeaf.type !== 'leaf') return

    const tabIdx = fromLeaf.tabs.findIndex(t => t.id === tabId)
    if (tabIdx < 0) return

    const spliced = fromLeaf.tabs.splice(tabIdx, 1)
    const tab = spliced[0]!

    // Same-leaf drag to own edge: if no tabs remain the pane would become empty.
    // Restore and bail — splitting a single-tab pane onto itself is a no-op.
    if (fromLeafId === targetLeafId && fromLeaf.tabs.length === 0) {
      fromLeaf.tabs.push(tab)
      return
    }

    // Remove the source pane from the tree WITHOUT calling closePane (which emits
    // an intermediate invalid state). We emit exactly once at the end.
    if (fromLeaf.tabs.length === 0 && fromLeafId !== targetLeafId) {
      const parent = findParent(this.splitTree, fromLeafId)
      if (parent) {
        const sibling = parent.first.id === fromLeafId ? parent.second : parent.first
        this.splitTree = replaceNode(this.splitTree, parent.id, sibling)
      }
    } else if (fromLeaf.activeTabIndex >= fromLeaf.tabs.length) {
      fromLeaf.activeTabIndex = Math.max(0, fromLeaf.tabs.length - 1)
    }

    const direction: SplitDirection = (edge === 'left' || edge === 'right') ? 'horizontal' : 'vertical'
    const position: 'first' | 'second' = (edge === 'left' || edge === 'top') ? 'first' : 'second'

    // Re-find target since the tree may have changed after source removal.
    const target = findNode(this.splitTree, targetLeafId)
    if (!target) {
      this.splitTree = {
        type: 'leaf',
        id: genId('leaf'),
        tabs: [tab],
        activeTabIndex: 0,
      }
      this.emit()
      return
    }

    const newLeaf: LeafNode = {
      type: 'leaf',
      id: genId('leaf'),
      tabs: [tab],
      activeTabIndex: 0,
    }

    const splitNode: SplitNode = {
      type: 'split',
      id: genId('split'),
      direction,
      ratio: 0.5,
      first: position === 'first' ? newLeaf : target,
      second: position === 'second' ? newLeaf : target,
    }

    this.splitTree = replaceNode(this.splitTree, targetLeafId, splitNode)
    this.emit()
  }

  // ── Side panel ──

  toggleSidePanel(panelId: string) {
    if (this.sidePanel.activePanelId === panelId && this.sidePanel.visible) {
      this.sidePanel = { ...this.sidePanel, visible: false }
    } else {
      this.sidePanel = { ...this.sidePanel, visible: true, activePanelId: panelId }
    }
    this.emit()
  }

  setSidePanelWidth(width: number) {
    this.sidePanel = {
      ...this.sidePanel,
      width: Math.max(200, Math.min(600, width)),
    }
    this.emit()
  }

  // ── Toolbar ──

  toggleToolbarItem(itemId: string) {
    const item = this.toolbarItems.find(i => i.id === itemId)
    if (!item) return
    item.visible = !item.visible
    this.emit()
  }

  resetToolbarDefaults() {
    this.toolbarItems = defaultToolbarItems()
    this.emit()
  }

  // ── Get first leaf (for adding tabs to default location) ──

  getFirstLeaf(): LeafNode | null {
    function walk(node: SplitTreeNode): LeafNode | null {
      if (node.type === 'leaf') return node
      return walk(node.first)
    }
    return walk(this.splitTree)
  }

  // ── View window management ──

  isViewOpen(viewType: ViewType): boolean {
    function walk(node: SplitTreeNode): boolean {
      if (node.type === 'leaf') {
        return node.tabs.some(t => t.viewType === viewType)
      }
      return walk(node.first) || walk(node.second)
    }
    return walk(this.splitTree)
  }
}
