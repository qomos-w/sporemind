// ── DockSizesStore: persists dock sizes in pixels ──
import { DOCK_DEFAULT_SIZE, DOCK_MIN_WIDTH, DOCK_MIN_HEIGHT } from '../types/layout';
const STORAGE_KEY = 'sporemind-dock-sizes-v1';
const VERSION = 1;
const SAVE_DEBOUNCE_MS = 500;
export class DockSizesStore {
    constructor(storage) {
        // Dock sizes in pixels
        Object.defineProperty(this, "leftWidth", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: DOCK_DEFAULT_SIZE
        });
        Object.defineProperty(this, "rightWidth", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: DOCK_DEFAULT_SIZE
        });
        Object.defineProperty(this, "bottomHeight", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: DOCK_DEFAULT_SIZE
        });
        // Inner splits as percentages
        Object.defineProperty(this, "leftTopPct", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: 0.5
        });
        Object.defineProperty(this, "rightTopPct", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: 0.5
        });
        Object.defineProperty(this, "bottomLeftPct", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: 0.5
        });
        // Last known window size in pixels
        Object.defineProperty(this, "windowW", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: 1400
        });
        Object.defineProperty(this, "windowH", {
            enumerable: true,
            configurable: true,
            writable: true,
            value: 900
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
        this.storage = storage;
    }
    /** Load persisted values. Call once before first render. */
    async init() {
        try {
            const raw = await this.storage.get(STORAGE_KEY);
            if (!raw)
                return;
            const parsed = JSON.parse(raw);
            if (parsed.version !== VERSION)
                return;
            this.leftWidth = Math.max(DOCK_MIN_WIDTH, parsed.leftWidth || this.leftWidth);
            this.rightWidth = Math.max(DOCK_MIN_WIDTH, parsed.rightWidth || this.rightWidth);
            this.bottomHeight = Math.max(DOCK_MIN_HEIGHT, parsed.bottomHeight || this.bottomHeight);
            this.leftTopPct = parsed.leftTopPct || this.leftTopPct;
            this.rightTopPct = parsed.rightTopPct || this.rightTopPct;
            this.bottomLeftPct = parsed.bottomLeftPct || this.bottomLeftPct;
            this.windowW = parsed.windowW || this.windowW;
            this.windowH = parsed.windowH || this.windowH;
        }
        catch {
            // Corrupt storage — keep defaults
        }
    }
    scheduleSave() {
        if (this.saveTimer)
            clearTimeout(this.saveTimer);
        this.saveTimer = setTimeout(() => {
            this.saveTimer = null;
            const data = {
                version: VERSION,
                leftWidth: this.leftWidth,
                rightWidth: this.rightWidth,
                bottomHeight: this.bottomHeight,
                leftTopPct: this.leftTopPct,
                rightTopPct: this.rightTopPct,
                bottomLeftPct: this.bottomLeftPct,
                windowW: this.windowW,
                windowH: this.windowH,
            };
            this.storage.set(STORAGE_KEY, JSON.stringify(data)).catch(() => { });
        }, SAVE_DEBOUNCE_MS);
    }
    setLeft(v) { this.leftWidth = v; this.scheduleSave(); }
    setRight(v) { this.rightWidth = v; this.scheduleSave(); }
    setBottom(v) { this.bottomHeight = v; this.scheduleSave(); }
    setLeftTop(v) { this.leftTopPct = v; this.scheduleSave(); }
    setRightTop(v) { this.rightTopPct = v; this.scheduleSave(); }
    setBottomLeft(v) { this.bottomLeftPct = v; this.scheduleSave(); }
    setWindowSize(w, h) {
        this.windowW = w;
        this.windowH = h;
        this.scheduleSave();
    }
}
