import { Window, Application, Events } from '@wailsio/runtime'
import { isWails } from './runtime'
import * as desktopApp from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { FileDragOutRequest } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/models'

export const isWailsAvailable = isWails

export function emitWailsEvent(name: string, data?: unknown): void {
  if (!isWailsAvailable()) return
  void Events.Emit(name, data)
}

export function onWailsEvent(name: string, callback: (...args: unknown[]) => void): (() => void) | null {
  if (!isWailsAvailable()) return null
  const cancel = Events.On(name, callback as (...args: any[]) => void)
  return () => cancel()
}

export function WindowMinimise(): void {
  if (!isWailsAvailable()) return
  void Window.Minimise()
}

export function WindowToggleMaximise(): void {
  if (!isWailsAvailable()) return
  void Window.ToggleMaximise()
}

export async function WindowIsMaximised(): Promise<boolean> {
  if (!isWailsAvailable()) return false
  try {
    return await Window.IsMaximised()
  } catch {
    return false
  }
}

export async function WindowGetSize(): Promise<{ w: number; h: number } | null> {
  if (!isWailsAvailable()) return null
  try {
    const [w, h] = await Promise.all([Window.Width(), Window.Height()])
    return { w, h }
  } catch {
    return null
  }
}

export async function WindowGetPosition(): Promise<{ x: number; y: number } | null> {
  if (!isWailsAvailable()) return null
  try {
    const p = await Window.Position()
    return { x: p.x, y: p.y }
  } catch {
    return null
  }
}

export function WindowMaximise(): void {
  if (!isWailsAvailable()) return
  void Window.Maximise()
}

export function WindowUnmaximise(): void {
  if (!isWailsAvailable()) return
  void Window.UnMaximise()
}

export function WindowSetSize(width: number, height: number): void {
  if (!isWailsAvailable()) return
  void Window.SetSize(width, height)
}

export function WindowSetPosition(x: number, y: number): void {
  if (!isWailsAvailable()) return
  void Window.SetPosition(x, y)
}

export function WindowFullscreen(): void {
  if (!isWailsAvailable()) return
  void Window.Fullscreen()
}

export function WindowUnfullscreen(): void {
  if (!isWailsAvailable()) return
  void Window.UnFullscreen()
}

export async function WindowIsFullscreen(): Promise<boolean> {
  if (!isWailsAvailable()) return false
  try {
    return await Window.IsFullscreen()
  } catch {
    return false
  }
}

export function Quit(): void {
  if (!isWailsAvailable()) return
  void Application.Quit()
}

/**
 * Start a native OS drag-out (copy-only) of the given local files and/or
 * extracted archive, via the generated desktop binding. Blocks until the drag
 * completes (drop or cancel; a cancel is a success). Desktop-only: outside the
 * Wails webview, or when the binding call fails, it degrades gracefully to
 * `false` so callers can fall back to the in-app HTML5 drag
 * (see startOsFileDragOut in ui/os-file-dnd).
 */
export async function StartFileDragOut(req: FileDragOutRequest): Promise<boolean> {
  if (!isWailsAvailable()) return false
  try {
    await desktopApp.StartFileDragOut(req)
    return true
  } catch {
    return false
  }
}
