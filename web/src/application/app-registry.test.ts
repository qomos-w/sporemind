import { describe, expect, it } from 'vitest'
import { appRegistry, normalizeApp, findRevokedAppTabs, type AppTabLike } from './app-registry'
import type { GosporeClient } from '@qomos/gospore-client'

function makeEvent(value: Partial<{
  Id: string
  Runtime: string
  Kind: string
  State: string
  Version: string
  Error: string
  Generation: number
  Entrypoints: Array<{ Id: string; Kind: string; Title: string }>
}>) {
  return {
    Id: value.Id ?? 'app.test',
    Runtime: value.Runtime ?? 'spore',
    Kind: value.Kind ?? 'running',
    State: value.State ?? 'running',
    Version: value.Version ?? '1',
    Error: value.Error,
    Generation: value.Generation,
    Entrypoints: value.Entrypoints ?? [],
  }
}

describe('appRegistry', () => {
  it('normalizes generated app status fields and entrypoints', () => {
    const app = normalizeApp({ Id: 'quick.example', Runtime: 'spore', State: 'running', Version: '1.0.0', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] })
    expect(app).toMatchObject({ id: 'quick.example', runtime: 'spore', state: 'running', version: '1.0.0' })
    expect(app.entrypoints[0]?.id).toBe('home')
  })

  it('normalizes the direct-HTTP BackendUrl for apps with an SDK listener', () => {
    const app = normalizeApp({ Id: 'direct.example', Runtime: 'native', State: 'running', Version: '1', BackendUrl: 'http://127.0.0.1:4100', Entrypoints: [] })
    expect(app.backendUrl).toBe('http://127.0.0.1:4100')
    // Apps without a listener keep the undefined fallback (gateway asset route).
    const legacy = normalizeApp({ Id: 'legacy.example', Runtime: 'native', State: 'running', Version: '1', Entrypoints: [] })
    expect(legacy.backendUrl).toBeUndefined()
  })

  it('normalizes permissions and granted capabilities', () => {
    const app = normalizeApp({
      Id: 'perm.example',
      Runtime: 'native',
      State: 'running',
      Version: '1.0.0',
      Permissions: ['fs.read', 'fs.write'],
      GrantedCapabilities: ['fs.read'],
      Entrypoints: [],
    })
    expect(app.permissions).toEqual(['fs.read', 'fs.write'])
    expect(app.grantedCapabilities).toEqual(['fs.read'])
  })

  it('upserts and removes lifecycle entries', () => {
    appRegistry.clear()
    appRegistry.upsert({ id: 'plugin.example', runtime: 'native', state: 'running', version: '1.0.0', entrypoints: [] })
    expect(appRegistry.get('plugin.example')?.runtime).toBe('native')
    appRegistry.remove('plugin.example')
    expect(appRegistry.getAll()).toHaveLength(0)
  })

  it('preserves entrypoints when lifecycle status omits them', () => {
    appRegistry.clear()
    appRegistry.upsert({ id: 'app.entry', runtime: 'spore', state: 'running', version: '1', entrypoints: [{ id: 'home', kind: 'view', title: 'Home' }] })
    appRegistry.upsert({ id: 'app.entry', runtime: 'spore', state: 'running', version: '1', entrypoints: [] })
    expect(appRegistry.get('app.entry')?.entrypoints).toHaveLength(1)
    appRegistry.remove('app.entry')
  })

  it('normalizes Generation and preserves it when a lifecycle event omits it', () => {
    appRegistry.clear()
    const fromStatus = normalizeApp({ Id: 'app.gen', Runtime: 'native', State: 'running', Version: '1', Entrypoints: [], Generation: 3 })
    expect(fromStatus.generation).toBe(3)
    appRegistry.upsert(fromStatus)
    // A plugin load/unload event carries no generation — it must not wipe it.
    appRegistry.upsert({ id: 'app.gen', runtime: 'native', state: 'stopped', version: '1', entrypoints: [] })
    expect(appRegistry.get('app.gen')?.generation).toBe(3)
    // A reload carries the bumped generation and replaces it.
    appRegistry.upsert({ id: 'app.gen', runtime: 'native', state: 'running', version: '1', entrypoints: [], generation: 4 })
    expect(appRegistry.get('app.gen')?.generation).toBe(4)
    appRegistry.remove('app.gen')
  })

  it('derives the icon from Bundles and preserves it across lifecycle events', () => {
    appRegistry.clear()
    const normalized = normalizeApp(makeEvent({ Id: 'app.iconic' }))
    appRegistry.upsert({ ...normalized, icon: 'server' })
    expect(appRegistry.get('app.iconic')?.icon).toBe('server')
    // Lifecycle events carry no Bundles — the icon must survive the upsert.
    appRegistry.upsert({ id: 'app.iconic', runtime: normalized.runtime, state: 'stopped', version: normalized.version, entrypoints: normalized.entrypoints })
    expect(appRegistry.get('app.iconic')?.icon).toBe('server')
    appRegistry.remove('app.iconic')
  })

  it('derives the color from the first colored Bundle and preserves it across lifecycle events', () => {
    // The first bundle declares no color; the later colored bundle must win.
    const colored = normalizeApp({
      Id: 'app.colored',
      Runtime: 'native',
      State: 'running',
      Version: '1',
      Entrypoints: [],
      Bundles: [
        { Title: 'Plain', Tools: [] },
        { Title: 'Search', Color: '#2563eb', Tools: [] },
      ],
    })
    expect(colored.color).toBe('#2563eb')
    // No bundle carries a color → the field stays empty.
    const plain = normalizeApp({
      Id: 'app.plain',
      Runtime: 'native',
      State: 'running',
      Version: '1',
      Entrypoints: [],
      Bundles: [{ Title: 'Plain', Tools: [] }],
    })
    expect(plain.color).toBeUndefined()
    appRegistry.clear()
    appRegistry.upsert(colored)
    // Lifecycle events carry no Bundles — the color must survive the upsert.
    appRegistry.upsert({ id: 'app.colored', runtime: colored.runtime, state: 'stopped', version: colored.version, entrypoints: colored.entrypoints })
    expect(appRegistry.get('app.colored')?.color).toBe('#2563eb')
    appRegistry.remove('app.colored')
  })

  it('ignores identical and stale lifecycle updates', () => {
    appRegistry.clear()
    const app = { id: 'app.lifecycle', runtime: 'native', state: 'running', version: '1.0.0', entrypoints: [] }
    appRegistry.upsert(app)
    const snapshot = appRegistry.getAll()
    appRegistry.upsert(app)
    expect(appRegistry.getAll()).toBe(snapshot)
    appRegistry.remove('app.lifecycle')
    expect(appRegistry.get('app.lifecycle')).toBeUndefined()
  })

  it('caches getAll() snapshot and invalidates on mutation', () => {
    appRegistry.clear()
    const snap1 = appRegistry.getAll()
    const snap2 = appRegistry.getAll()
    expect(snap1).toBe(snap2)
    appRegistry.upsert({ id: 'app.a', runtime: 'spore', state: 'running', version: '1', entrypoints: [] })
    const snap3 = appRegistry.getAll()
    expect(snap3).not.toBe(snap1)
    expect(snap3).toHaveLength(1)
    appRegistry.remove('app.a')
    expect(appRegistry.getAll()).toHaveLength(0)
  })

  it('keeps the newer generation when a stale snapshot response races a lifecycle event', async () => {
    appRegistry.clear()
    // Subscription-first ordering: the reloaded event (generation 5) can land
    // while the list fetch — started earlier against pre-attach state — is
    // still in flight. The stale response carries generation 4 and must not
    // downgrade the remount key the event already applied.
    let resolveList: ((value: any) => void) | undefined
    const fakeClient = {
      invoke: () => new Promise<any>(resolve => { resolveList = resolve }),
      subscribe: async function* () {
        yield makeEvent({ Kind: 'reloaded', State: 'running', Generation: 5 })
      },
    } as unknown as GosporeClient

    const loadPromise = appRegistry.load(fakeClient)
    const controller = new AbortController()
    const watchPromise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await watchPromise
    expect(appRegistry.get('app.test')?.generation).toBe(5)

    resolveList?.({ Items: [makeEvent({ Kind: 'running', State: 'running', Generation: 4 })] })
    await loadPromise
    expect(appRegistry.get('app.test')?.generation).toBe(5)
    appRegistry.remove('app.test')
  })

  it('deduplicates duplicate lifecycle events within the dedup window', async () => {
    appRegistry.clear()
    const events = [
      makeEvent({ State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
      makeEvent({ State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
      makeEvent({ State: 'running', Version: '2', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
    ]

    let listIndex = 0
    const snapshots = [events[0], events[2]]
    const fakeClient = {
      invoke: async () => ({ Items: [snapshots[Math.min(listIndex++, snapshots.length - 1)]] }),
      subscribe: async function* () {
        for (const ev of events) yield ev
      },
    } as unknown as GosporeClient

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    // Allow the async generator to consume all events.
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    expect(appRegistry.getAll()).toHaveLength(1)
    expect(appRegistry.get('app.test')?.version).toBe('2')
    expect(appRegistry.getEntrypoints()).toHaveLength(1)
  })

  it('re-fetches the full list when a lifecycle event creates an entry without entrypoints', async () => {
    appRegistry.clear()
    let listCalls = 0
    const fakeClient = {
      invoke: async () => {
        listCalls++
        // First load (startup snapshot): the app is not registered yet.
        // Resync after the lifecycle event: the full record with its view.
        return listCalls === 1
          ? { Items: [] }
          : { Items: [makeEvent({ Id: 'app.late', Kind: 'running', State: 'running', Entrypoints: [{ Id: 'main', Kind: 'view', Title: 'Main' }] })] }
      },
      subscribe: async function* () {
        yield makeEvent({ Id: 'app.late', Kind: 'running', State: 'running', Entrypoints: [] })
      },
    } as unknown as GosporeClient

    await appRegistry.load(fakeClient)
    expect(appRegistry.get('app.late')).toBeUndefined()

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    // The event created an entrypoint-less entry; the debounced resync must
    // backfill entrypoints so the launcher tile appears without a restart.
    await new Promise((resolve) => setTimeout(resolve, 400))
    const stored = appRegistry.get('app.late')
    expect(stored?.state).toBe('running')
    expect(stored?.entrypoints).toHaveLength(1)
    expect(stored?.entrypoints[0]?.kind).toBe('view')
    appRegistry.remove('app.late')
  })

  it('cleans up views/panels/commands when an app unloads', async () => {
    appRegistry.clear()
    const events = [
      makeEvent({ Kind: 'running', State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }, { Id: 'cmd', Kind: 'command', Title: 'Run' }] }),
      makeEvent({ Kind: 'unloaded', State: 'unloaded' }),
    ]

    const fakeClient = {
      invoke: async () => ({ Items: [events[0]] }),
      subscribe: async function* () {
        for (const ev of events) yield ev
      },
    } as unknown as GosporeClient

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    expect(appRegistry.get('app.test')).toBeUndefined()
    expect(appRegistry.getAll()).toHaveLength(0)
    expect(appRegistry.getEntrypoints()).toHaveLength(0)
  })

  it('removes the app on the unloaded lifecycle event and swallows duplicate unloads', async () => {
    appRegistry.clear()
    const events = [
      makeEvent({ Kind: 'running', State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
      makeEvent({ Kind: 'unloaded', State: 'unloaded' }),
      makeEvent({ Kind: 'unloaded', State: 'unloaded' }),
    ]

    const fakeClient = {
      invoke: async () => ({ Items: [] }),
      subscribe: async function* () {
        for (const ev of events) yield ev
      },
    } as unknown as GosporeClient

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    expect(appRegistry.get('app.test')).toBeUndefined()
    expect(appRegistry.getAll()).toHaveLength(0)
  })

  it('keeps the app on plugin_unload (stopped / unload_pending) lifecycle events', async () => {
    appRegistry.clear()
    const events = [
      makeEvent({ Kind: 'running', State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
      makeEvent({ Kind: 'stopped', State: 'stopped' }),
      makeEvent({ Kind: 'unload_pending', State: 'unload_pending', Error: 'in-process plugin unloaded' }),
    ]

    const fakeClient = {
      invoke: async () => ({ Items: [] }),
      subscribe: async function* () {
        for (const ev of events) yield ev
      },
    } as unknown as GosporeClient

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    expect(appRegistry.get('app.test')).toBeDefined()
    expect(appRegistry.get('app.test')?.state).toBe('unload_pending')
    expect(appRegistry.getAll()).toHaveLength(1)
  })

  it('updates the entry state and stores the crash cause on a crashed lifecycle event', async () => {
    appRegistry.clear()
    const events = [
      makeEvent({ Kind: 'running', State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
      makeEvent({ Kind: 'crashed', State: 'crashed', Error: 'plugin process exited: signal: segmentation fault' }),
      makeEvent({ Kind: 'crashed', State: 'crashed', Error: 'plugin process exited: signal: segmentation fault' }),
    ]

    const fakeClient = {
      invoke: async () => ({ Items: [] }),
      subscribe: async function* () {
        for (const ev of events) yield ev
      },
    } as unknown as GosporeClient

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    const entry = appRegistry.get('app.test')
    expect(entry).toBeDefined()
    expect(entry?.state).toBe('crashed')
    expect(entry?.error).toBe('plugin process exited: signal: segmentation fault')
    // The crashed entry keeps its view entrypoints and stays in the registry.
    expect(entry?.entrypoints).toHaveLength(1)
    expect(appRegistry.getAll()).toHaveLength(1)
    appRegistry.remove('app.test')
  })

  it('updates the entry state and stores the load error on a failed lifecycle event', async () => {
    appRegistry.clear()
    // Distinct id: the lifecycle dedup map is only cleared by clear() when
    // the registry is non-empty, so reusing 'app.test' would let the
    // previous test's running-event key swallow this test's first event.
    const events = [
      makeEvent({ Id: 'app.failed', Kind: 'running', State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
      makeEvent({ Id: 'app.failed', Kind: 'failed', State: 'failed', Error: 'open F:\\gone\\plugin.exe: The system cannot find the path specified.' }),
    ]

    const fakeClient = {
      invoke: async () => ({ Items: [] }),
      subscribe: async function* () {
        for (const ev of events) yield ev
      },
    } as unknown as GosporeClient

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    const entry = appRegistry.get('app.failed')
    expect(entry).toBeDefined()
    expect(entry?.state).toBe('failed')
    expect(entry?.error).toContain('cannot find the path')
    expect(entry?.entrypoints).toHaveLength(1)
    expect(appRegistry.getAll()).toHaveLength(1)
    appRegistry.remove('app.failed')
  })

  it('reconciles away apps that disappear from the server list', async () => {
    appRegistry.clear()
    appRegistry.upsert({ id: 'app.vanishing', runtime: 'spore', state: 'running', version: '1', entrypoints: [] })
    expect(appRegistry.getAll()).toHaveLength(1)

    const fakeClient = {
      invoke: async () => ({ Items: [] }),
      subscribe: async function* () { /* never yields */ },
    } as unknown as GosporeClient

    await appRegistry.load(fakeClient)

    expect(appRegistry.get('app.vanishing')).toBeUndefined()
    expect(appRegistry.getAll()).toHaveLength(0)
  })

  it('getEntrypoints only exposes entrypoints of running apps', () => {
    appRegistry.clear()
    appRegistry.upsert({ id: 'run.app', runtime: 'spore', state: 'running', version: '1', entrypoints: [{ id: 'main', kind: 'view', title: 'Main' }] })
    appRegistry.upsert({ id: 'update.app', runtime: 'spore', state: 'restart_pending', version: '1', entrypoints: [{ id: 'main', kind: 'view', title: 'Main' }] })
    appRegistry.upsert({ id: 'stop.app', runtime: 'spore', state: 'stopped', version: '1', entrypoints: [{ id: 'main', kind: 'view', title: 'Main' }] })
    appRegistry.upsert({ id: 'fail.app', runtime: 'spore', state: 'failed', version: '1', entrypoints: [{ id: 'main', kind: 'view', title: 'Main' }] })
    expect(appRegistry.getEntrypoints()).toHaveLength(1)
    expect(appRegistry.getEntrypoints()[0]?.id).toBe('main')
    appRegistry.remove('run.app')
    expect(appRegistry.getEntrypoints()).toHaveLength(0)
    appRegistry.clear()
  })

  it('keeps restart_pending apps in the registry (update, not removal)', async () => {
    appRegistry.clear()
    const events = [
      makeEvent({ Runtime: 'native', State: 'running', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
      makeEvent({ Runtime: 'native', State: 'restart_pending', Version: '2', Entrypoints: [{ Id: 'home', Kind: 'view', Title: 'Home' }] }),
    ]
    const snapshots = [events[0], events[1]]
    let listIndex = 0
    const fakeClient = {
      invoke: async () => ({ Items: [snapshots[Math.min(listIndex++, snapshots.length - 1)]] }),
      subscribe: async function* () {
        for (const ev of events) yield ev
      },
    } as unknown as GosporeClient

    const controller = new AbortController()
    const promise = appRegistry.watchLifecycle(fakeClient, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 50))
    controller.abort()
    await promise

    // restart_pending is an in-registry update, not a removal…
    expect(appRegistry.get('app.test')?.state).toBe('restart_pending')
    expect(appRegistry.getAll()).toHaveLength(1)
    // …but its entrypoints are no longer launchable (non-running).
    expect(appRegistry.getEntrypoints()).toHaveLength(0)
    appRegistry.clear()
  })

  it('does not swallow restart_pending arriving after an unloaded record', () => {
    appRegistry.clear()
    appRegistry.upsert({ id: 'app.u', runtime: 'native', state: 'unloaded', version: '1', entrypoints: [] })
    appRegistry.upsert({ id: 'app.u', runtime: 'native', state: 'restart_pending', version: '2', entrypoints: [] })
    expect(appRegistry.get('app.u')?.state).toBe('restart_pending')
    appRegistry.clear()
  })

  it('still lets running updates pass the stale-unloaded guard', () => {
    appRegistry.clear()
    appRegistry.upsert({ id: 'app.u', runtime: 'native', state: 'unloaded', version: '1', entrypoints: [] })
    appRegistry.upsert({ id: 'app.u', runtime: 'native', state: 'running', version: '2', entrypoints: [] })
    expect(appRegistry.get('app.u')?.state).toBe('running')
    appRegistry.clear()
  })

  it('still drops redundant unloaded echoes', () => {
    appRegistry.clear()
    appRegistry.upsert({ id: 'app.u', runtime: 'native', state: 'unloaded', version: '1', entrypoints: [] })
    appRegistry.upsert({ id: 'app.u', runtime: 'native', state: 'stopped', version: '1', entrypoints: [] })
    expect(appRegistry.get('app.u')?.state).toBe('unloaded')
    appRegistry.clear()
  })
})

describe('findRevokedAppTabs', () => {
  const PREFIX = 'plugin:'
  const appWithHome = {
    id: 'quick.demo',
    runtime: 'spore',
    state: 'running',
    version: '1',
    entrypoints: [{ id: 'home', kind: 'view', title: 'Home' }],
  }

  function pluginTab(appID: string, viewID: string): AppTabLike {
    return { id: `${PREFIX}${viewID}`, viewType: `${PREFIX}${viewID}`, metadata: { pluginID: appID, route: '/' } }
  }

  it('revokes tabs whose app has been unregistered', () => {
    const tabs = [pluginTab('quick.demo', 'home')]
    expect(findRevokedAppTabs(tabs, PREFIX, [])).toEqual([`${PREFIX}home`])
  })

  it('keeps tabs whose app and view entrypoint are still registered', () => {
    const tabs = [pluginTab('quick.demo', 'home')]
    expect(findRevokedAppTabs(tabs, PREFIX, [appWithHome])).toEqual([])
  })

  it('revokes tabs whose view entrypoint was removed by a reload', () => {
    const reloaded = { ...appWithHome, entrypoints: [{ id: 'settings', kind: 'view', title: 'Settings' }] }
    const tabs = [pluginTab('quick.demo', 'home'), pluginTab('quick.demo', 'settings')]
    expect(findRevokedAppTabs(tabs, PREFIX, [reloaded])).toEqual([`${PREFIX}home`])
  })

  it('keeps tabs when the app entrypoint list is transiently empty', () => {
    const transient = { ...appWithHome, entrypoints: [] as typeof appWithHome.entrypoints }
    const tabs = [pluginTab('quick.demo', 'home')]
    expect(findRevokedAppTabs(tabs, PREFIX, [transient])).toEqual([])
  })

  it('ignores non-app tabs', () => {
    const tabs: AppTabLike[] = [
      { id: 'view:home', viewType: 'home' },
      { id: 'file:/a.go', viewType: 'file', metadata: { filePath: '/a.go' } },
    ]
    expect(findRevokedAppTabs(tabs, PREFIX, [])).toEqual([])
  })
})
