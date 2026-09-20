// WindowController — abstracts native window control for the title bar.
//
// In a Wails (or other host-based) build the consumer wires this to the
// runtime bindings; in pure browser mode the no-op controller is fine.

export interface WindowController {
  /** Returns true when running inside a host shell (Wails, Tauri, Electron, …). */
  isHostMode(): boolean
  minimise(): Promise<void>
  toggleMaximise(): Promise<void>
  isMaximised(): Promise<boolean>
  getSize(): Promise<{ w: number; h: number } | null>
  getPosition(): Promise<{ x: number; y: number } | null>
  quit(): Promise<void>
  fullscreen(): Promise<void>
  unfullscreen(): Promise<void>
  isFullscreen(): Promise<boolean>
}

/** Pure-browser fallback: no-ops for host-only methods, native API for fullscreen. */
export const browserNoOpController: WindowController = {
  isHostMode() { return false },
  async minimise() {},
  async toggleMaximise() {},
  async isMaximised() { return false },
  async getSize() { return null },
  async getPosition() { return null },
  async quit() {},
  async fullscreen() {
    if (document.documentElement.requestFullscreen) {
      await document.documentElement.requestFullscreen()
    }
  },
  async unfullscreen() {
    if (document.exitFullscreen) {
      await document.exitFullscreen()
    }
  },
  async isFullscreen() {
    return Boolean(document.fullscreenElement)
  },
}
