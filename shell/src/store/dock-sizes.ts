// ── DockSizesStore: persists dock sizes in pixels ──

import type { StorageAdapter } from '../adapters/storage'
import { DOCK_DEFAULT_SIZE, DOCK_MIN_WIDTH, DOCK_MIN_HEIGHT } from '../types/layout'

const STORAGE_KEY = 'sporemind-dock-sizes-v1'
const VERSION = 1
const SAVE_DEBOUNCE_MS = 500

interface PersistedData {
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

export class DockSizesStore {
  // Dock sizes in pixels
  leftWidth      = DOCK_DEFAULT_SIZE
  rightWidth     = DOCK_DEFAULT_SIZE
  bottomHeight   = DOCK_DEFAULT_SIZE
  // Inner splits as percentages
  leftTopPct     = 0.5
  rightTopPct    = 0.5
  bottomLeftPct  = 0.5
  // Last known window size in pixels
  windowW = 1400
  windowH = 900

  private saveTimer: ReturnType<typeof setTimeout> | null = null
  private storage: StorageAdapter

  constructor(storage: StorageAdapter) {
    this.storage = storage
  }

  /** Load persisted values. Call once before first render. */
  async init(): Promise<void> {
    try {
      const raw = await this.storage.get(STORAGE_KEY)
      if (!raw) return
      const parsed: PersistedData = JSON.parse(raw)
      if (parsed.version !== VERSION) return

      this.leftWidth      = Math.max(DOCK_MIN_WIDTH,  parsed.leftWidth      || this.leftWidth)
      this.rightWidth     = Math.max(DOCK_MIN_WIDTH,  parsed.rightWidth     || this.rightWidth)
      this.bottomHeight   = Math.max(DOCK_MIN_HEIGHT, parsed.bottomHeight   || this.bottomHeight)
      this.leftTopPct     = parsed.leftTopPct     || this.leftTopPct
      this.rightTopPct    = parsed.rightTopPct    || this.rightTopPct
      this.bottomLeftPct  = parsed.bottomLeftPct  || this.bottomLeftPct
      this.windowW        = parsed.windowW        || this.windowW
      this.windowH        = parsed.windowH        || this.windowH
    } catch {
      // Corrupt storage — keep defaults
    }
  }

  private scheduleSave() {
    if (this.saveTimer) clearTimeout(this.saveTimer)
    this.saveTimer = setTimeout(() => {
      this.saveTimer = null
      const data: PersistedData = {
        version: VERSION,
        leftWidth:      this.leftWidth,
        rightWidth:     this.rightWidth,
        bottomHeight:   this.bottomHeight,
        leftTopPct:     this.leftTopPct,
        rightTopPct:    this.rightTopPct,
        bottomLeftPct:  this.bottomLeftPct,
        windowW:        this.windowW,
        windowH:        this.windowH,
      }
      this.storage.set(STORAGE_KEY, JSON.stringify(data)).catch(() => {})
    }, SAVE_DEBOUNCE_MS)
  }

  setLeft(v: number)          { this.leftWidth = v;       this.scheduleSave() }
  setRight(v: number)         { this.rightWidth = v;      this.scheduleSave() }
  setBottom(v: number)        { this.bottomHeight = v;    this.scheduleSave() }
  setLeftTop(v: number)       { this.leftTopPct = v;      this.scheduleSave() }
  setRightTop(v: number)      { this.rightTopPct = v;     this.scheduleSave() }
  setBottomLeft(v: number)    { this.bottomLeftPct = v;   this.scheduleSave() }
  setWindowSize(w: number, h: number) {
    this.windowW = w
    this.windowH = h
    this.scheduleSave()
  }
}
