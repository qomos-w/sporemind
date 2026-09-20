import { client } from './generated-client'
import * as browserClient from '../gen-clients/browsermanager/client'
import type { BrowserManagerCreateReq, BrowserManagerUpdateReq, BrowserManagerOpenGlobalReq } from '../gen-types/browser'
import type { BrowserManagerEvent } from '../gen-types/browser'
import type { BrowserCookieEntry } from '../gen-types/browser.cookie'

const LIST_RETRIES = 2
const LIST_RETRY_DELAY_MS = 500

export async function listBrowserWindows() {
  let lastErr: unknown
  for (let i = 0; i <= LIST_RETRIES; i++) {
    try {
      const resp = await browserClient.list(client)
      return resp.Items ?? []
    } catch (e) {
      lastErr = e
      const msg = e instanceof Error ? e.message : String(e)
      if (i < LIST_RETRIES && msg.includes('timed out')) {
        await new Promise((r) => setTimeout(r, LIST_RETRY_DELAY_MS))
        continue
      }
      throw e
    }
  }
  throw lastErr
}

export type BrowserProxyMode = 'system' | 'none' | 'custom'

/** Effective proxy mode of a config; legacy empty ProxyMode maps Proxy!= '' to custom. */
export function effectiveProxyMode(cfg: { Proxy: string; ProxyMode: string }): BrowserProxyMode {
  if (cfg.ProxyMode === 'none') return 'none'
  if (cfg.ProxyMode === 'custom') return cfg.Proxy ? 'custom' : 'system'
  return cfg.Proxy ? 'custom' : 'system'
}

/** Signature of the effective proxy ("system" | "none" | "custom:<url>"), used to detect live proxy changes. */
export function proxySignature(cfg: { Proxy: string; ProxyMode: string }): string {
  const mode = effectiveProxyMode(cfg)
  return mode === 'custom' ? `custom:${cfg.Proxy}` : mode
}

export async function openBrowserWindow(name: string, url: string, proxy = '', proxyMode: BrowserProxyMode = 'system') {
  const req: BrowserManagerCreateReq = { Name: name, Url: url, Proxy: proxy, ProxyMode: proxyMode, Hidden: false }
  return browserClient.create(client, req)
}

export async function closeBrowserWindow(id: string) {
  await browserClient.remove(client, { Id: id })
}

export async function closeBrowserInstance(id: string) {
  const instances = await listBrowserWindows()
  const inst = instances.find((i) => i.Config.Id === id)
  if (!inst) return
  const cfg = { ...inst.Config, Open: false }
  await updateBrowserWindow({ Id: id, Config: cfg })
}

export async function openBrowserInstance(id: string) {
  const instances = await listBrowserWindows()
  const inst = instances.find((i) => i.Config.Id === id)
  if (!inst) return
  const cfg = { ...inst.Config, Open: true }
  await updateBrowserWindow({ Id: id, Config: cfg })
}

// Persist the CURRENT navigation page (State.Url) for an independent window.
// Config.Url is the settings page (home) and must never be touched here.
export async function updateBrowserWindowUrl(id: string, url: string) {
  const instances = await listBrowserWindows()
  const inst = instances.find((i) => i.Config.Id === id)
  if (!inst) return
  const cfg = {
    ...inst.Config,
    State: { ...inst.Config.State, Url: url },
  }
  await updateBrowserWindow({ Id: id, Config: cfg })
}

export async function updateBrowserWindow(req: BrowserManagerUpdateReq) {
  return browserClient.update(client, req)
}

export async function navigateBrowserWindow(id: string, url: string) {
  return browserClient.navigate(client, { Id: id, Url: url })
}

export async function openGlobalBrowser(url: string) {
  const req: BrowserManagerOpenGlobalReq = { Url: url }
  return browserClient.openGlobal(client, req)
}

export function onBrowserManagerEvent(handler: (e: BrowserManagerEvent) => void): () => void {
  return browserClient.OnBrowserManagerEvent(client, handler)
}

/** Export cookies of a browser instance, grouped by domain. */
export async function exportBrowserCookies(id: string): Promise<Record<string, BrowserCookieEntry[]>> {
  const resp = await browserClient.exportCookies(client, { Id: id })
  return resp.Cookies ?? {}
}

/** Import cookies into a browser instance; returns the number of cookies written. */
export async function importBrowserCookies(id: string, cookies: Record<string, BrowserCookieEntry[]>): Promise<number> {
  const resp = await browserClient.importCookies(client, { Id: id, Cookies: cookies })
  return resp.Imported
}
