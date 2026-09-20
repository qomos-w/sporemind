import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { DesktopWindowState } from '../gen-types/desktop'
import type { InstallSourceSelection, ScreenshotData, WindowInfo } from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/models'
import { isWails } from './runtime'

type WailsBindings = typeof desktop

export type { InstallSourceSelection, ScreenshotData, WindowInfo }

// Splash gate: browser child windows should not appear until the splash screen
// has finished. BrowserView.open() awaits this before showing the window.
// In non-Wails mode it resolves immediately.
let _resolveSplashDone: () => void = () => {}
const _splashDone = isWails() ? new Promise<void>(resolve => { _resolveSplashDone = resolve }) : Promise.resolve()

export function notifySplashDone() { _resolveSplashDone() }
export function splashDone(): Promise<void> { return _splashDone }

export const hasWailsBindings = isWails

export async function getBindings(): Promise<WailsBindings | null> {
  if (!hasWailsBindings()) return null
  return desktop
}

export async function selectFolder(): Promise<string> {
  if (!hasWailsBindings()) return ''
  return desktop.SelectFolder()
}

export async function selectFile(): Promise<string> {
  if (!hasWailsBindings()) return ''
  return desktop.SelectFile()
}

// Windows IFileDialog cannot pick files and folders in one dialog
// (FOS_PICKFOLDERS greys out files), so the install flow uses one picker
// per source kind: .zip file vs package directory.
export async function selectAppPackageFile(): Promise<InstallSourceSelection | null> {
  if (!hasWailsBindings()) return null
  return desktop.SelectAppPackageFile()
}

export async function selectAppPackageFolder(): Promise<InstallSourceSelection | null> {
  if (!hasWailsBindings()) return null
  return desktop.SelectAppPackageFolder()
}

export async function fetchDesktopWindowState(): Promise<DesktopWindowState | null> {
  if (!hasWailsBindings()) return null
  return desktop.DesktopWindowState()
}

export async function saveDesktopWindowState(state: DesktopWindowState): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.SaveDesktopWindowState(state)
}

export function openDevTools(): void {
  if (!hasWailsBindings()) return
  void desktop.OpenDevTools()
}

export async function sendLog(level: string, msg: string, caller = '', fields?: Record<string, unknown>): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.Log(level, msg, caller, fields ?? ({} as Record<string, unknown>))
}

export async function setClipboardText(text: string): Promise<void> {
  if (!hasWailsBindings()) {
    await navigator.clipboard.writeText(text)
    return
  }
  await desktop.SetClipboardText(text)
}

export async function setClipboardImage(dataURL: string): Promise<void> {
  if (!hasWailsBindings()) {
    try {
      const resp = await fetch(dataURL)
      const blob = await resp.blob()
      await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })])
    } catch { /* web clipboard may not support images */ }
    return
  }
  await desktop.SetClipboardImage(dataURL)
}

export async function startScreenshot(): Promise<ScreenshotData | null> {
  if (!hasWailsBindings()) return null
  const data = await desktop.StartScreenshot()
  return data as ScreenshotData | null
}

export async function getScreenshotData(): Promise<ScreenshotData | null> {
  if (!hasWailsBindings()) return null
  const data = await desktop.GetScreenshotData()
  return data as ScreenshotData | null
}

export async function refreshScreenshotForWindow(handle: number): Promise<ScreenshotData | null> {
  if (!hasWailsBindings()) return null
  const data = await desktop.RefreshScreenshotForWindow(handle)
  return data as ScreenshotData | null
}

export async function closeScreenshotWindow(): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.CloseScreenshotWindow()
}

export async function showScreenshotWindow(): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.ShowScreenshotWindow()
}

export async function saveScreenshotDialog(base64Data: string, defaultName: string): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.SaveScreenshotDialog(base64Data, defaultName)
}

// Expose EvalJSResult on window so ExecJS-injected scripts can route results
// back to the backend. Registered at module init so it is ready before any
// /debug/eval-js request can fire.
if (typeof window !== 'undefined' && hasWailsBindings()) {
  ;(window as any).__sporemindEvalJSResult = desktop.EvalJSResult
}

// Right panel browser child window (Wails desktop only)
export async function openBrowserSession(sessionId: string, kind: string, url: string, x: number, y: number, width: number, height: number): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.OpenBrowserSession(sessionId, kind, url, x, y, width, height)
}

export async function updateBrowserWindow(sessionId: string, x: number, y: number, width: number, height: number): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.UpdateBrowserWindow(sessionId, x, y, width, height)
}

export async function navigateBrowserSession(sessionId: string, url: string): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.NavigateBrowserSession(sessionId, url)
}

export async function goBackBrowserWindow(sessionId: string): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.BrowserHistoryBack(sessionId)
}

export async function goForwardBrowserWindow(sessionId: string): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.BrowserHistoryForward(sessionId)
}

export async function closeBrowserSession(sessionId: string): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.CloseBrowserSession(sessionId)
}

export async function hideAllBrowserWindows(): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.HideAllBrowserWindows()
}

export async function showAllBrowserWindows(): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.ShowAllBrowserWindows()
}

export async function destroyAllBrowserWindows(): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.DestroyAllBrowserWindows()
}

export async function setBrowserWindowVisible(sessionId: string, visible: boolean): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.SetBrowserWindowVisible(sessionId, visible)
}

export async function captureBrowserWindow(sessionId: string): Promise<string> {
  if (!hasWailsBindings()) return ''
  return await desktop.CaptureBrowserWindow(sessionId)
}

export async function setBrowserColorScheme(mode: 'light' | 'dark'): Promise<void> {
  if (!hasWailsBindings()) return
  await desktop.SetBrowserColorScheme(mode)
}

/** Poll until Wails runtime IPC is ready or deadline expires.
 *  After a webview refresh the runtime objects exist but the IPC bridge
 *  may not be fully wired — we verify by calling a lightweight Go method
 *  (DesktopWindowState). Resolves true if ready, false if not Wails or timed out. */
export function waitWailsReady(deadlineMs = 6000): Promise<boolean> {
  return new Promise((resolve) => {
    if (!isWails()) { resolve(false); return }
    const start = Date.now()
    const check = async () => {
      try {
        await Promise.race([
          desktop.DesktopWindowState(),
          new Promise<never>((_, rej) =>
            setTimeout(() => rej(new Error('ping timeout')), 1500),
          ),
        ])
        resolve(true)
        return
      } catch {
        // IPC not ready yet, retry
      }
      if (Date.now() - start > deadlineMs) { resolve(false); return }
      setTimeout(check, 200)
    }
    void check()
  })
}
