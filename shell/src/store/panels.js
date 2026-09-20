// ── PanelManager: manages floating/pinned/side panel state ──
import { clampToViewport } from '../lib/dock-utils';
const STORAGE_KEY = 'sporemind-panels-v1';
const PANELS_VERSION = 2;
const SAVE_DEBOUNCE_MS = 300;
// Floating panels start at z-index 400 (above --z-overlay:300)
let nextZ = 400;
export class PanelManager {
    constructor(storage, defaultPanels) {
        Object.defineProperty(this, "panels", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: void 0
        });
        Object.defineProperty(this, "zoneTabOrder", {
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
        this.panels = new Map();
        this.zoneTabOrder = new Map();
        if (defaultPanels) {
            for (const [id, state] of Object.entries(defaultPanels)) {
                this.panels.set(id, { ...state });
            }
            // Build initial zoneTabOrder from default panels
            this.rebuildZoneTabOrder();
        }
        if (typeof window !== 'undefined') {
            window.addEventListener('resize', () => this.clampAll());
        }
    }
    // ── Zone tab order helpers ──
    rebuildZoneTabOrder() {
        this.zoneTabOrder.clear();
        for (const [id, p] of this.panels) {
            if (p.mode === 'pinned' && p.dockZone) {
                const arr = this.zoneTabOrder.get(p.dockZone);
                if (arr)
                    arr.push(id);
                else
                    this.zoneTabOrder.set(p.dockZone, [id]);
            }
        }
    }
    addToZoneOrder(zone, id) {
        const arr = this.zoneTabOrder.get(zone);
        if (arr) {
            if (!arr.includes(id))
                arr.push(id);
        }
        else {
            this.zoneTabOrder.set(zone, [id]);
        }
    }
    removeFromZoneOrder(zone, id) {
        const arr = this.zoneTabOrder.get(zone);
        if (!arr)
            return;
        const idx = arr.indexOf(id);
        if (idx >= 0)
            arr.splice(idx, 1);
        if (arr.length === 0)
            this.zoneTabOrder.delete(zone);
    }
    /** Load persisted state from storage. Call once at app startup before first render. */
    async init() {
        try {
            const raw = await this.storage.get(STORAGE_KEY);
            if (!raw) {
                console.info('[panels] init: no persisted state found');
                return;
            }
            const parsed = JSON.parse(raw);
            if (parsed.version !== PANELS_VERSION) {
                console.warn('[panels] init: version mismatch', {
                    expected: PANELS_VERSION,
                    actual: parsed.version,
                });
                return;
            }
            for (const [id, state] of Object.entries(parsed.panels)) {
                const s = { ...state };
                if (s.mode === 'pinned' && !s.dockZone) {
                    s.dockZone = 'left-top';
                }
                this.panels.set(id, s);
            }
            if (parsed.zoneTabOrder) {
                for (const [zone, ids] of Object.entries(parsed.zoneTabOrder)) {
                    const valid = ids.filter(id => {
                        const p = this.panels.get(id);
                        return p && p.mode === 'pinned' && p.dockZone === zone;
                    });
                    const missing = [...this.panels.entries()]
                        .filter(([, p]) => p.mode === 'pinned' && p.dockZone === zone)
                        .map(([id]) => id)
                        .filter(id => !valid.includes(id));
                    const normalized = [...valid, ...missing];
                    if (normalized.length > 0)
                        this.zoneTabOrder.set(zone, normalized);
                }
            }
            if (this.zoneTabOrder.size === 0) {
                this.rebuildZoneTabOrder();
            }
            const activated = new Set();
            for (const [, p] of this.panels) {
                if (!p.visible || !p.dockZone || p.mode !== 'pinned')
                    continue;
                if (activated.has(p.dockZone)) {
                    p.visible = false;
                }
                else {
                    activated.add(p.dockZone);
                }
            }
            console.info('[panels] init: restored state', {
                panelCount: this.panels.size,
                zoneCount: this.zoneTabOrder.size,
            });
            this._version++;
        }
        catch (error) {
            console.error('[panels] init: failed to restore state', error);
        }
    }
    scheduleSave() {
        if (this.saveTimer)
            clearTimeout(this.saveTimer);
        this.saveTimer = setTimeout(() => {
            this.saveTimer = null;
            const panels = {};
            for (const [id, state] of this.panels) {
                panels[id] = { ...state };
            }
            const zoneTabOrder = {};
            for (const [zone, ids] of this.zoneTabOrder) {
                zoneTabOrder[zone] = [...ids];
            }
            this.storage.set(STORAGE_KEY, JSON.stringify({ version: PANELS_VERSION, panels, zoneTabOrder })).catch(() => { });
        }, SAVE_DEBOUNCE_MS);
    }
    emit() {
        this._version++;
        for (const fn of this.listeners)
            fn();
        this.scheduleSave();
    }
    get(id) {
        return this.panels.get(id);
    }
    // ── Zone query methods ──
    /** Get all panel IDs in a zone (ordered by zoneTabOrder) */
    getZonePanels(zone) {
        if (!zone)
            return [];
        const ordered = this.zoneTabOrder.get(zone);
        if (ordered && ordered.length > 0)
            return [...ordered];
        // Fallback: iterate panels map
        const result = [];
        for (const [id, p] of this.panels) {
            if (p.mode === 'pinned' && p.dockZone === zone)
                result.push(id);
        }
        return result;
    }
    /** Get the active (visible) tab ID in a zone, or null */
    getActiveTabId(zone) {
        if (!zone)
            return null;
        for (const [id, p] of this.panels) {
            if (p.mode === 'pinned' && p.dockZone === zone && p.visible)
                return id;
        }
        return null;
    }
    /** Activate a tab in its zone (deactivate others in the same zone) */
    activateTab(id) {
        const p = this.panels.get(id);
        if (!p || p.mode !== 'pinned' || !p.dockZone)
            return;
        for (const [otherId, other] of this.panels) {
            if (otherId !== id && other.dockZone === p.dockZone && other.mode === 'pinned' && other.visible) {
                other.visible = false;
            }
        }
        p.visible = true;
        this.emit();
    }
    /** Move a pinned panel to a different zone and activate it there */
    moveToZone(id, targetZone) {
        const p = this.panels.get(id);
        if (!p)
            return;
        const oldZone = p.dockZone;
        // Remove from old zone order
        if (oldZone)
            this.removeFromZoneOrder(oldZone, id);
        p.dockZone = targetZone;
        p.visible = true;
        // Add to target zone order
        this.addToZoneOrder(targetZone, id);
        // Deactivate others in target zone
        for (const [otherId, other] of this.panels) {
            if (otherId !== id && other.dockZone === targetZone && other.mode === 'pinned' && other.visible) {
                other.visible = false;
            }
        }
        // Activate a remaining panel in the old zone
        if (oldZone && oldZone !== targetZone) {
            const remaining = this.getZonePanels(oldZone);
            if (remaining.length > 0 && !this.panels.get(remaining[0])?.visible) {
                this.panels.get(remaining[0]).visible = true;
            }
        }
        this.emit();
    }
    /** Reorder tabs within a zone */
    reorderInZone(zone, fromIndex, toIndex) {
        if (!zone)
            return;
        const tabs = this.zoneTabOrder.get(zone);
        if (!tabs)
            return;
        if (fromIndex < 0 || fromIndex >= tabs.length)
            return;
        const spliced = tabs.splice(fromIndex, 1);
        const moved = spliced[0];
        tabs.splice(toIndex, 0, moved);
        this.emit();
    }
    // ── Panel mutations ──
    /** Clamp all visible floating panels to viewport */
    clampAll() {
        let changed = false;
        for (const [, p] of this.panels) {
            if (p.mode !== 'floating' || !p.visible)
                continue;
            const clamped = clampToViewport(p.pos, p.size);
            if (clamped.x !== p.pos.x || clamped.y !== p.pos.y) {
                p.pos = clamped;
                changed = true;
            }
            const vw = window.innerWidth;
            const vh = window.innerHeight;
            if (p.size.w > vw) {
                p.size.w = vw;
                changed = true;
            }
            if (p.size.h > vh) {
                p.size.h = vh;
                changed = true;
            }
        }
        if (changed)
            this.emit();
    }
    togglePin(id) {
        const p = this.panels.get(id);
        if (p.mode === 'pinned') {
            const oldZone = p.dockZone;
            p.mode = 'floating';
            p.dockZone = null;
            p.pos = clampToViewport({ x: 100, y: 80 }, p.size);
            // Remove from zone order
            if (oldZone)
                this.removeFromZoneOrder(oldZone, id);
            // Activate next panel in the old zone
            if (oldZone) {
                const remaining = this.getZonePanels(oldZone);
                if (remaining.length > 0) {
                    this.panels.get(remaining[0]).visible = true;
                }
            }
        }
        else {
            p.mode = 'pinned';
            if (!p.dockZone) {
                p.dockZone = 'left-top';
            }
            // Add to zone order
            this.addToZoneOrder(p.dockZone, id);
        }
        this.emit();
    }
    dock(id, zone) {
        const p = this.panels.get(id);
        const oldZone = p.dockZone;
        // Remove from old zone order
        if (oldZone)
            this.removeFromZoneOrder(oldZone, id);
        p.mode = 'pinned';
        p.dockZone = zone;
        p.visible = true;
        // Add to target zone order
        this.addToZoneOrder(zone, id);
        // Deactivate others in target zone
        if (zone) {
            for (const [otherId, other] of this.panels) {
                if (otherId !== id && other.dockZone === zone && other.mode === 'pinned' && other.visible) {
                    other.visible = false;
                }
            }
        }
        // Activate next panel in the old zone
        if (oldZone && oldZone !== zone) {
            const remaining = this.getZonePanels(oldZone);
            if (remaining.length > 0) {
                this.panels.get(remaining[0]).visible = true;
            }
        }
        this.emit();
    }
    /** Register a panel that wasn't in the constructor defaults (e.g. added via HMR). */
    register(id, state) {
        if (this.panels.has(id))
            return;
        this.panels.set(id, { ...state });
        if (state.mode === 'pinned' && state.dockZone) {
            this.addToZoneOrder(state.dockZone, id);
        }
    }
    showInZone(id) {
        this.activateTab(id);
    }
    show(id, pos) {
        const p = this.panels.get(id);
        if (!p)
            return;
        if (p.mode === 'pinned' && p.dockZone) {
            this.activateTab(id);
            return;
        }
        p.visible = true;
        if (pos) {
            p.pos = clampToViewport(pos, p.size);
        }
        p.zIndex = ++nextZ;
        this.emit();
    }
    hide(id) {
        const p = this.panels.get(id);
        p.visible = false;
        this.emit();
    }
    move(id, pos) {
        const p = this.panels.get(id);
        p.pos = clampToViewport(pos, p.size);
    }
    commitMove(_id) {
        this.emit();
    }
    resize(id, size, pos) {
        const p = this.panels.get(id);
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        p.size = { w: Math.min(size.w, vw), h: Math.min(size.h, vh) };
        if (pos) {
            p.pos = clampToViewport(pos, p.size);
        }
        else if (p.mode === 'floating') {
            p.pos = clampToViewport(p.pos, p.size);
        }
        this.emit();
    }
    bringToFront(id) {
        const p = this.panels.get(id);
        p.zIndex = ++nextZ;
        this.emit();
    }
    subscribe(fn) {
        this.listeners.add(fn);
        return () => { this.listeners.delete(fn); };
    }
}
