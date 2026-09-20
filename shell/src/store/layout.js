// ── LayoutStore: manages split tree, toolbar, side panel with storage persistence ──
const STORAGE_KEY = 'sporemind-layout-v1';
const LAYOUT_VERSION = 1;
const SAVE_DEBOUNCE_MS = 300;
let nextId = 1;
function genId(prefix) {
    return `${prefix}-${nextId++}`;
}
// ── Default state ──
function defaultSplitTree() {
    return {
        type: 'leaf',
        id: genId('leaf'),
        tabs: [
            { id: 'tab-home', viewType: 'home', label: 'Home', closable: false },
        ],
        activeTabIndex: 0,
    };
}
function defaultToolbarItems() {
    return [];
}
function defaultSidePanel() {
    return { visible: false, width: 300, activePanelId: null };
}
// ── Tree traversal helpers ──
function findNode(tree, id) {
    if (tree.id === id)
        return tree;
    if (tree.type === 'split') {
        return findNode(tree.first, id) || findNode(tree.second, id);
    }
    return null;
}
function findParent(tree, id) {
    if (tree.type === 'split') {
        if (tree.first.id === id || tree.second.id === id)
            return tree;
        return findParent(tree.first, id) || findParent(tree.second, id);
    }
    return null;
}
/** Replace a node in the tree by id, returning new root */
function replaceNode(tree, targetId, replacement) {
    if (tree.id === targetId)
        return replacement;
    if (tree.type === 'split') {
        return {
            ...tree,
            first: replaceNode(tree.first, targetId, replacement),
            second: replaceNode(tree.second, targetId, replacement),
        };
    }
    return tree;
}
// ── LayoutStore ──
export class LayoutStore {
    constructor(storage) {
        Object.defineProperty(this, "splitTree", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        Object.defineProperty(this, "toolbarItems", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        Object.defineProperty(this, "sidePanel", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        Object.defineProperty(this, "listeners", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: new Set()
        });
        Object.defineProperty(this, "_version", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: 0
        });
        Object.defineProperty(this, "saveTimer", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: null
        });
        Object.defineProperty(this, "storage", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        Object.defineProperty(this, "getVersion", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: () => this._version
        });
        this.storage = storage;
        this.splitTree = defaultSplitTree();
        this.toolbarItems = defaultToolbarItems();
        this.sidePanel = defaultSidePanel();
    }
    /** Load persisted state from storage. Call once at app startup before first render. */
    async init() {
        try {
            const raw = await this.storage.get(STORAGE_KEY);
            if (!raw)
                return;
            const parsed = JSON.parse(raw);
            if (parsed.version !== LAYOUT_VERSION)
                return;
            this.splitTree = parsed.splitTree;
            this.toolbarItems = parsed.toolbarItems;
            this.sidePanel = parsed.sidePanel;
            this._version++;
        }
        catch {
            // Corrupt or missing — keep defaults, no emit needed
        }
    }
    scheduleSave() {
        if (this.saveTimer)
            clearTimeout(this.saveTimer);
        this.saveTimer = setTimeout(() => {
            this.saveTimer = null;
            const state = {
                version: LAYOUT_VERSION,
                splitTree: this.splitTree,
                sidePanel: this.sidePanel,
                toolbarItems: this.toolbarItems,
            };
            this.storage.set(STORAGE_KEY, JSON.stringify(state)).catch(() => { });
        }, SAVE_DEBOUNCE_MS);
    }
    emit() {
        this._version++;
        for (const fn of this.listeners)
            fn();
        this.scheduleSave();
    }
    subscribe(fn) {
        this.listeners.add(fn);
        return () => { this.listeners.delete(fn); };
    }
    // ── Split tree operations ──
    splitPane(leafId, direction, newTab, position = 'second') {
        const leaf = findNode(this.splitTree, leafId);
        if (!leaf || leaf.type !== 'leaf')
            return;
        const newLeaf = {
            type: 'leaf',
            id: genId('leaf'),
            tabs: [newTab],
            activeTabIndex: 0,
        };
        const splitNode = {
            type: 'split',
            id: genId('split'),
            direction,
            ratio: 0.5,
            first: position === 'first' ? newLeaf : leaf,
            second: position === 'second' ? newLeaf : leaf,
        };
        this.splitTree = replaceNode(this.splitTree, leafId, splitNode);
        this.emit();
    }
    closePane(leafId) {
        const parent = findParent(this.splitTree, leafId);
        if (!parent)
            return; // Root leaf — don't close
        const sibling = parent.first.id === leafId ? parent.second : parent.first;
        this.splitTree = replaceNode(this.splitTree, parent.id, sibling);
        this.emit();
    }
    resizeSplit(splitId, ratio) {
        const node = findNode(this.splitTree, splitId);
        if (!node || node.type !== 'split')
            return;
        node.ratio = Math.max(0.1, Math.min(0.9, ratio));
        this.emit();
    }
    // ── Tab operations ──
    addTab(leafId, tab) {
        const leaf = findNode(this.splitTree, leafId);
        if (!leaf || leaf.type !== 'leaf')
            return;
        const existingIdx = leaf.tabs.findIndex(t => t.id === tab.id);
        if (existingIdx >= 0) {
            // Merge metadata (and any other scalar fields) so reopening an already-
            // open file with a new line number still propagates the new state to the
            // mounted editor.
            leaf.tabs[existingIdx] = { ...leaf.tabs[existingIdx], ...tab };
            leaf.activeTabIndex = existingIdx;
        }
        else {
            leaf.tabs.push(tab);
            leaf.activeTabIndex = leaf.tabs.length - 1;
        }
        this.emit();
    }
    removeTab(leafId, tabId) {
        const leaf = findNode(this.splitTree, leafId);
        if (!leaf || leaf.type !== 'leaf')
            return;
        const idx = leaf.tabs.findIndex(t => t.id === tabId);
        if (idx < 0)
            return;
        leaf.tabs.splice(idx, 1);
        if (leaf.tabs.length === 0) {
            this.closePane(leafId);
            return;
        }
        if (leaf.activeTabIndex >= leaf.tabs.length) {
            leaf.activeTabIndex = leaf.tabs.length - 1;
        }
        this.emit();
    }
    setActiveTab(leafId, index) {
        const leaf = findNode(this.splitTree, leafId);
        if (!leaf || leaf.type !== 'leaf')
            return;
        if (index < 0 || index >= leaf.tabs.length)
            return;
        leaf.activeTabIndex = index;
        this.emit();
    }
    moveTab(fromLeafId, tabId, toLeafId, insertIndex) {
        const fromLeaf = findNode(this.splitTree, fromLeafId);
        const toLeaf = findNode(this.splitTree, toLeafId);
        if (!fromLeaf || !toLeaf || fromLeaf.type !== 'leaf' || toLeaf.type !== 'leaf')
            return;
        const tabIdx = fromLeaf.tabs.findIndex(t => t.id === tabId);
        if (tabIdx < 0)
            return;
        const spliced = fromLeaf.tabs.splice(tabIdx, 1);
        const tab = spliced[0];
        const idx = insertIndex !== undefined ? insertIndex : toLeaf.tabs.length;
        toLeaf.tabs.splice(idx, 0, tab);
        toLeaf.activeTabIndex = idx;
        if (fromLeaf.tabs.length === 0) {
            this.closePane(fromLeafId);
            return;
        }
        if (fromLeaf.activeTabIndex >= fromLeaf.tabs.length) {
            fromLeaf.activeTabIndex = fromLeaf.tabs.length - 1;
        }
        this.emit();
    }
    moveTabToEdge(fromLeafId, tabId, targetLeafId, edge) {
        const fromLeaf = findNode(this.splitTree, fromLeafId);
        if (!fromLeaf || fromLeaf.type !== 'leaf')
            return;
        const tabIdx = fromLeaf.tabs.findIndex(t => t.id === tabId);
        if (tabIdx < 0)
            return;
        const spliced = fromLeaf.tabs.splice(tabIdx, 1);
        const tab = spliced[0];
        // Same-leaf drag to own edge: if no tabs remain the pane would become empty.
        // Restore and bail — splitting a single-tab pane onto itself is a no-op.
        if (fromLeafId === targetLeafId && fromLeaf.tabs.length === 0) {
            fromLeaf.tabs.push(tab);
            return;
        }
        // Remove the source pane from the tree WITHOUT calling closePane (which emits
        // an intermediate invalid state). We emit exactly once at the end.
        if (fromLeaf.tabs.length === 0 && fromLeafId !== targetLeafId) {
            const parent = findParent(this.splitTree, fromLeafId);
            if (parent) {
                const sibling = parent.first.id === fromLeafId ? parent.second : parent.first;
                this.splitTree = replaceNode(this.splitTree, parent.id, sibling);
            }
        }
        else if (fromLeaf.activeTabIndex >= fromLeaf.tabs.length) {
            fromLeaf.activeTabIndex = Math.max(0, fromLeaf.tabs.length - 1);
        }
        const direction = (edge === 'left' || edge === 'right') ? 'horizontal' : 'vertical';
        const position = (edge === 'left' || edge === 'top') ? 'first' : 'second';
        // Re-find target since the tree may have changed after source removal.
        const target = findNode(this.splitTree, targetLeafId);
        if (!target) {
            this.splitTree = {
                type: 'leaf',
                id: genId('leaf'),
                tabs: [tab],
                activeTabIndex: 0,
            };
            this.emit();
            return;
        }
        const newLeaf = {
            type: 'leaf',
            id: genId('leaf'),
            tabs: [tab],
            activeTabIndex: 0,
        };
        const splitNode = {
            type: 'split',
            id: genId('split'),
            direction,
            ratio: 0.5,
            first: position === 'first' ? newLeaf : target,
            second: position === 'second' ? newLeaf : target,
        };
        this.splitTree = replaceNode(this.splitTree, targetLeafId, splitNode);
        this.emit();
    }
    // ── Side panel ──
    toggleSidePanel(panelId) {
        if (this.sidePanel.activePanelId === panelId && this.sidePanel.visible) {
            this.sidePanel = { ...this.sidePanel, visible: false };
        }
        else {
            this.sidePanel = { ...this.sidePanel, visible: true, activePanelId: panelId };
        }
        this.emit();
    }
    setSidePanelWidth(width) {
        this.sidePanel = {
            ...this.sidePanel,
            width: Math.max(200, Math.min(600, width)),
        };
        this.emit();
    }
    // ── Toolbar ──
    toggleToolbarItem(itemId) {
        const item = this.toolbarItems.find(i => i.id === itemId);
        if (!item)
            return;
        item.visible = !item.visible;
        this.emit();
    }
    resetToolbarDefaults() {
        this.toolbarItems = defaultToolbarItems();
        this.emit();
    }
    // ── Get first leaf (for adding tabs to default location) ──
    getFirstLeaf() {
        function walk(node) {
            if (node.type === 'leaf')
                return node;
            return walk(node.first);
        }
        return walk(this.splitTree);
    }
    // ── View window management ──
    isViewOpen(viewType) {
        function walk(node) {
            if (node.type === 'leaf') {
                return node.tabs.some(t => t.viewType === viewType);
            }
            return walk(node.first) || walk(node.second);
        }
        return walk(this.splitTree);
    }
}
