import type { GosporeClient } from '@qomos/gospore-client'
import { list } from '../gen-clients/appmanager/client'
import { subscribeService } from '../gen-clients/gospore.events/client'
import type { AppBundle, AppCallableDescriptor, AppEventDescriptor, AppObjectDescriptor } from '../gen-types/app'
import { removeAppSchemas, replaceAllAppSchemas } from '../codec/dynamic-schema-registry'

export type AppRuntime = 'spore' | 'native' | string

export interface AppEntry {
  id: string
  // Manifest display name; labels the sidebar's virtual per-app directory
  // that groups this app's dedicated plugin agents.
  name?: string
  runtime: AppRuntime
  state: string
  version: string
  kind?: string
  namespace?: string
  packageHash?: string
  artifactHash?: string
  // Session-generation counter (1 on register, incremented on every
  // reload). Drives the plugin iframe cache-busting URL param and remount.
  generation?: number
  // Direct-HTTP data path: the plugin process's own HTTP origin when it runs
  // an SDK listener (AppStatus.BackendUrl). Absent → gateway asset fallback.
  backendUrl?: string
  isolation?: string
  trustClass?: string
  schemaDescriptors?: Record<string, AppObjectDescriptor>
  callables?: AppCallableDescriptor[]
  events?: AppEventDescriptor[]
  bundles?: AppBundle[]
  error?: string
  entrypoints: Array<{ id: string; kind: string; title: string; route?: string; zone?: string }>
  permissions?: string[]
  grantedCapabilities?: string[]
  icon?: string
  // Bundle-declared hex color (first bundle that carries one); "" / undefined
  // when no bundle declares a color. Drives the colored app/bundle glyph.
  color?: string
}

export type AppRegistryListener = () => void

function normalizeApp(value: any): AppEntry {
  return {
    id: String(value?.Id ?? value?.id ?? ''),
    name: value?.Name ?? value?.name,
    runtime: String(value?.Runtime ?? value?.runtime ?? ''),
    state: String(value?.State ?? value?.state ?? ''),
    version: String(value?.Version ?? value?.version ?? ''),
    kind: value?.Kind ?? value?.kind,
    namespace: value?.Namespace ?? value?.namespace,
    packageHash: value?.PackageHash ?? value?.packageHash,
    artifactHash: value?.ArtifactHash ?? value?.artifactHash,
    generation: value?.Generation ?? value?.generation,
    backendUrl: value?.BackendUrl ?? value?.backendUrl,
    isolation: value?.Abi?.Isolation ?? value?.abi?.isolation,
    trustClass: value?.Abi?.TrustClass ?? value?.abi?.trustClass,
    schemaDescriptors: value?.SchemaDescriptors ?? value?.schemaDescriptors,
    callables: value?.Callables ?? value?.callables,
    events: value?.Events ?? value?.events,
    bundles: value?.Bundles ?? value?.bundles,
    error: value?.Error ?? value?.error,
    entrypoints: Array.isArray(value?.Entrypoints ?? value?.entrypoints) ? (value.Entrypoints ?? value.entrypoints).map((entry: any) => ({
      id: String(entry?.Id ?? entry?.id ?? ''),
      kind: String(entry?.Kind ?? entry?.kind ?? ''),
      title: String(entry?.Title ?? entry?.title ?? ''),
      route: entry?.Route ?? entry?.route,
      zone: entry?.Zone ?? entry?.zone,
    })) : [],
    permissions: Array.isArray(value?.Permissions ?? value?.permissions)
      ? (value.Permissions ?? value.permissions).map((p: any) => String(p))
      : [],
    grantedCapabilities: Array.isArray(value?.GrantedCapabilities ?? value?.grantedCapabilities)
      ? (value.GrantedCapabilities ?? value.grantedCapabilities).map((c: any) => String(c))
      : [],
    icon: (value?.Bundles ?? value?.bundles)?.find?.((b: any) => b?.Icon ?? b?.icon)?.Icon ?? (value?.Bundles ?? value?.bundles)?.find?.((b: any) => b?.Icon ?? b?.icon)?.icon,
    // Color comes from the first bundle that declares one (mirrors the icon
    // rule), so a later colored bundle is not shadowed by an earlier plain one.
    color: (value?.Bundles ?? value?.bundles)?.find?.((b: any) => b?.Color ?? b?.color)?.Color ?? (value?.Bundles ?? value?.bundles)?.find?.((b: any) => b?.Color ?? b?.color)?.color,
  }
}

class AppRegistry {
  private apps = new Map<string, AppEntry>()
  private listeners = new Set<AppRegistryListener>()
  private allSnapshot: AppEntry[] | null = null
  // True once the initial `list` snapshot has been applied. Consumers (e.g.
  // the right-panel plugin-tab restore) use it to distinguish apps that were
  // already running at boot from apps started mid-session.
  private loadedOnce = false

  // Deduplicate rapid / duplicate lifecycle events that may arrive from both
  // the projection watch and the event stream. Keys time out after a short
  // window so a legitimate re-transition to the same state is not swallowed.
  private seenLifecycle = new Map<string, number>()
  private readonly LIFECYCLE_DEDUP_MS = 5000
  private readonly MAX_DEDUP_KEYS = 128

  subscribe(listener: AppRegistryListener): () => void { this.listeners.add(listener); return () => this.listeners.delete(listener) }
  private notify() { this.allSnapshot = null; for (const listener of this.listeners) listener() }
  upsert(app: AppEntry): void {
    if (!app.id) return
    const previous = this.apps.get(app.id)
    // Guard against stale 'unloaded' echoes clobbering a newer load cycle:
    // 'running' and 'restart_pending' both mean the app is (about to be)
    // re-activated, so they must pass through; every other incoming state is
    // treated as a redundant unloaded echo and dropped.
    if (previous && previous.state === 'unloaded' && app.state !== 'running' && app.state !== 'restart_pending') return
    if (previous && app.entrypoints.length === 0 && previous.entrypoints.length > 0) {
      app = { ...app, entrypoints: previous.entrypoints }
    }
    // Lifecycle events carry no Callables/Events/Bundles (only id/state/...);
    // a state change would otherwise blank the protocol detail the toolbar
    // inspector shows. Keep the last full snapshot until a full load replaces it.
    if (previous && app.callables === undefined && previous.callables !== undefined) {
      app = { ...app, callables: previous.callables }
    }
    if (previous && app.events === undefined && previous.events !== undefined) {
      app = { ...app, events: previous.events }
    }
    if (previous && app.bundles === undefined && previous.bundles !== undefined) {
      app = { ...app, bundles: previous.bundles }
    }
    if (previous && app.schemaDescriptors === undefined && previous.schemaDescriptors !== undefined) {
      app = { ...app, schemaDescriptors: previous.schemaDescriptors }
    }
    // Lifecycle events carry no Bundles, so a state change would blank the
    // icon resolved from the initial list snapshot; keep it until the next
    // full load replaces it.
    if (previous && app.icon === undefined && previous.icon !== undefined) {
      app = { ...app, icon: previous.icon }
    }
    // Same as icon: lifecycle events carry no Bundles, so a state change would
    // blank the bundle-derived color; keep it until the next full load.
    if (previous && app.color === undefined && previous.color !== undefined) {
      app = { ...app, color: previous.color }
    }
    // Events that don't carry a generation (plugin load/unload, orphan
    // cleanup) must not wipe the known generation — losing it would remount
    // mounted iframes back to key 0.
    if (previous && app.generation === undefined && previous.generation !== undefined) {
      app = { ...app, generation: previous.generation }
    }
    if (previous && JSON.stringify(previous) === JSON.stringify(app)) return
    this.apps.set(app.id, app)
    this.notify()
  }
  remove(id: string): void {
    if (this.apps.delete(id)) {
      removeAppSchemas(id)
      this.notify()
    }
  }
  clear(): void {
    if (this.resyncTimer !== null) {
      clearTimeout(this.resyncTimer)
      this.resyncTimer = null
    }
    this.loadedOnce = false
    if (this.apps.size) {
      this.apps.clear()
      replaceAllAppSchemas([])
      this.seenLifecycle.clear()
      this.notify()
    }
  }
  get(id: string): AppEntry | undefined { return this.apps.get(id) }
  /** Whether the initial `list` snapshot has landed (see {@link load}). */
  isLoaded(): boolean { return this.loadedOnce }
  getAll(): AppEntry[] {
    if (this.allSnapshot === null) {
      this.allSnapshot = Array.from(this.apps.values())
    }
    return this.allSnapshot
  }
  /**
   * Entrypoints of running apps only. Non-running states (stopped, unloaded,
   * restart_pending, failed, cleanup transients) must not be launchable from
   * the omnibox/launcher — a stale entrypoint would start a dead app.
   */
  getEntrypoints(): AppEntry['entrypoints'] {
    return this.getAll().filter(app => app.state === 'running').flatMap(app => app.entrypoints)
  }

  /** Last client used by load/watch — stored for retry/re-sync. */
  private lastClient: GosporeClient | null = null
  private resyncTimer: ReturnType<typeof setTimeout> | null = null

  async load(client: GosporeClient): Promise<void> {
    this.lastClient = client
    const response = await list(client, {})
    this.replaceSnapshot(response.Items)
    // Notify even when the snapshot is unchanged: isLoaded() flips false→true
    // and useSyncExternalStore subscribers must re-read it.
    if (!this.loadedOnce) {
      this.loadedOnce = true
      this.notify()
    }
  }

  /** Re-load from the last known client. Used as a retry handler. */
  async sync(): Promise<void> {
    if (this.lastClient) await this.load(this.lastClient)
  }

  async watch(_client: GosporeClient, signal?: AbortSignal): Promise<void> {
    if (!signal) return
    await new Promise<void>(resolve => signal.addEventListener('abort', () => resolve(), { once: true }))
  }

  async watchLifecycle(client: GosporeClient, signal?: AbortSignal, onLifecycle?: (app: AppEntry) => void): Promise<void> {
    for await (const event of subscribeService(client, { serviceName: 'appmanager', kind: 'app_lifecycle' })) {
      if (signal?.aborted) return
      const app = normalizeApp(event)
      if (!app.id) continue
      if (this.isDuplicateLifecycle(app)) continue
      // 'unloaded' is emitted exclusively by finishCleanup and means the app
      // has been unregistered. Everything else (running, stopped, crashed,
      // restart_pending, unload_pending, ...) is an in-registry update;
      // 'crashed' in particular must reach upsert so the entry carries the
      // real state and the crash cause (entry.error) to every consumer.
      if (app.kind === 'unloaded') this.remove(app.id)
      else {
        this.upsert(app)
        // Lifecycle events carry only id/runtime/state/version. When an app
        // is registered after the startup snapshot, the event-created entry
        // has no entrypoints/icon, hiding it from the launcher and omnibox.
        // A debounced full-list re-fetch backfills the complete record.
        if ((this.get(app.id)?.entrypoints.length ?? 0) === 0) this.scheduleResync()
      }
      onLifecycle?.(app)
    }
  }

  private scheduleResync(): void {
    if (this.resyncTimer !== null) return
    this.resyncTimer = setTimeout(() => {
      this.resyncTimer = null
      void this.sync()
    }, 300)
  }

  private isDuplicateLifecycle(app: AppEntry): boolean {
    const key = `${app.id}|${app.state}|${app.version}|${app.error ?? ''}|${app.generation ?? ''}`
    const now = Date.now()
    const last = this.seenLifecycle.get(key)
    if (last !== undefined && now - last < this.LIFECYCLE_DEDUP_MS) {
      return true
    }
    this.seenLifecycle.set(key, now)
    // Prune oldest keys if the cache grows too large.
    if (this.seenLifecycle.size > this.MAX_DEDUP_KEYS) {
      let oldestKey: string | undefined
      let oldestTime = Infinity
      for (const [k, t] of this.seenLifecycle) {
        if (t < oldestTime) {
          oldestTime = t
          oldestKey = k
        }
      }
      if (oldestKey !== undefined) this.seenLifecycle.delete(oldestKey)
    }
    return false
  }

  private replaceSnapshot(snapshot: readonly any[]): void {
    const next = new Map<string, AppEntry>()
    for (const value of snapshot ?? []) { const app = normalizeApp(value); if (app.id) next.set(app.id, app) }
    // With the lifecycle subscription live before the snapshot fetch, an
    // event can land while the fetch is in flight and apply a newer
    // generation; the stale snapshot response must not downgrade it — the
    // generation drives iframe remount keys, and a downgrade would remount
    // panels back to an older document.
    for (const [id, prev] of this.apps) {
      const snap = next.get(id)
      if (!snap || (prev.generation ?? 0) <= (snap.generation ?? 0)) continue
      next.set(id, { ...snap, generation: prev.generation })
    }
    replaceAllAppSchemas(Array.from(next.values()))
    if (JSON.stringify(Array.from(this.apps.values())) === JSON.stringify(Array.from(next.values()))) return
    this.apps = next
    this.notify()
  }
}

export const appRegistry = new AppRegistry()
export { normalizeApp }

export function getAppCallable(appID: string, callID: string): AppCallableDescriptor | undefined {
  return appRegistry.get(appID)?.callables?.find(c => c.Id === callID)
}

export function getAppEvent(appID: string, eventKind: string): AppEventDescriptor | undefined {
  return appRegistry.get(appID)?.events?.find(e => e.Id === eventKind)
}

export const EMPTY_APP_ENTRIES: AppEntry[] = []

/** Minimal structural view of an open UI tab, decoupled from the shell types. */
export interface AppTabLike {
  id: string
  viewType: string
  metadata?: Record<string, string>
}

/**
 * Compute which open app-view tabs must be closed because their owning app
 * (or the specific view entrypoint) is no longer registered. This is the
 * UI-side revocation half of `appmanager.unregister`: once an app unloads,
 * its views must not remain open.
 *
 * A tab is revoked when:
 *   - its `metadata.pluginID` app is absent from `apps` (unregistered), or
 *   - the app still exists and declares view entrypoints, but none matches
 *     the tab's view id (removed by a reload).
 *
 * Apps with an empty entrypoint list are treated as unknown/transient and do
 * not cause revocation, to avoid closing tabs on partial lifecycle updates.
 */
export function findRevokedAppTabs(
  tabs: readonly AppTabLike[],
  viewTypePrefix: string,
  apps: readonly AppEntry[],
): string[] {
  const revoked: string[] = []
  for (const tab of tabs) {
    const pluginID = tab.metadata?.pluginID
    if (!pluginID || !tab.viewType.startsWith(viewTypePrefix)) continue
    const viewID = tab.viewType.slice(viewTypePrefix.length)
    const app = apps.find(candidate => candidate.id === pluginID)
    if (!app) {
      revoked.push(tab.id)
      continue
    }
    const viewEntries = app.entrypoints.filter(entry => entry.kind === 'view')
    if (viewEntries.length > 0 && !viewEntries.some(entry => entry.id === viewID)) {
      revoked.push(tab.id)
    }
  }
  return revoked
}
