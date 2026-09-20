import type { WindowController } from '@qomos/sporemind-shell'
import * as wailsRuntime from './wails-runtime'
import { isWails } from './runtime'

function isHostMode(): boolean {
  return isWails()
}

export const wailsWindowController: WindowController & {
  setSize: (width: number, height: number) => Promise<void>
  setPosition: (x: number, y: number) => Promise<void>
  maximise: () => Promise<void>
  unmaximise: () => Promise<void>
} = {
  isHostMode,

  async minimise() {
    wailsRuntime.WindowMinimise()
  },

  async toggleMaximise() {
    wailsRuntime.WindowToggleMaximise()
  },

  async maximise() {
    wailsRuntime.WindowMaximise()
  },

  async unmaximise() {
    wailsRuntime.WindowUnmaximise()
  },

  async isMaximised() {
    return wailsRuntime.WindowIsMaximised()
  },

  async getSize() {
    return wailsRuntime.WindowGetSize()
  },

  async setSize(width: number, height: number) {
    wailsRuntime.WindowSetSize(width, height)
  },

  async getPosition() {
    return wailsRuntime.WindowGetPosition()
  },

  async setPosition(x: number, y: number) {
    wailsRuntime.WindowSetPosition(x, y)
  },

  async quit() {
    wailsRuntime.Quit()
  },

  async fullscreen() {
    if (isHostMode()) {
      wailsRuntime.WindowFullscreen()
      return
    }
    if (document.documentElement.requestFullscreen) {
      await document.documentElement.requestFullscreen()
    }
  },

  async unfullscreen() {
    if (isHostMode()) {
      wailsRuntime.WindowUnfullscreen()
      return
    }
    if (document.exitFullscreen) {
      await document.exitFullscreen()
    }
  },

  async isFullscreen() {
    if (isHostMode()) {
      return wailsRuntime.WindowIsFullscreen()
    }
    return Boolean(document.fullscreenElement)
  },
}
