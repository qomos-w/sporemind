/**
 * Tool guide manager — lightweight singleton for ToolGuideOverlay visibility.
 *
 * Usage:
 *   import { toolGuideManager } from './tool-guide-manager'
 *   toolGuideManager.showToolGuide()  // show the full-screen tool guide
 *   toolGuideManager.hideToolGuide()  // dismiss
 *
 * The manager follows the same pub/sub pattern as `guideManager` but is much
 * simpler: it only tracks a boolean `visible` flag. The overlay component
 * subscribes and re-renders on changes.
 *
 * Wiring (help menu, onboarding completion callback, etc.) is NOT part of this
 * module — external callers simply call `showToolGuide()` / `hideToolGuide()`.
 */

export interface ToolGuideState {
  visible: boolean
}

type ToolGuideListener = (state: ToolGuideState) => void

class ToolGuideManager {
  private state: ToolGuideState = { visible: false }
  private listeners = new Set<ToolGuideListener>()

  private emit(): void {
    for (const l of this.listeners) l(this.state)
  }

  subscribe(listener: ToolGuideListener): () => void {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  getState(): Readonly<ToolGuideState> {
    return this.state
  }

  showToolGuide(): void {
    if (this.state.visible) return
    this.state = { visible: true }
    this.emit()
  }

  hideToolGuide(): void {
    if (!this.state.visible) return
    this.state = { visible: false }
    this.emit()
  }
}

export const toolGuideManager = new ToolGuideManager()
