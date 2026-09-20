import { describe, it, expect, vi, beforeEach } from 'vitest'
import { cardIdFromTitle } from '../../domain/mono-types'
import { monoStore } from './mono-store'

vi.mock('../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../gen-clients/project/client', () => ({
  wikiGetOpenCards: vi.fn(),
  wikiSaveOpenCards: vi.fn().mockResolvedValue({ OpenCards: [] }),
  wikiListCards: vi.fn().mockResolvedValue({ Cards: [] }),
  wikiGetCard: vi.fn().mockResolvedValue({ Raw: '' }),
  wikiCreateCard: vi.fn().mockResolvedValue({ Card: {} }),
  wikiEditCard: vi.fn().mockResolvedValue({ Card: {} }),
  wikiDeleteCard: vi.fn().mockResolvedValue({}),
  wikiCloseCard: vi.fn().mockResolvedValue({ OpenCards: [] }),
  wikiTriggerTimerCard: vi.fn().mockResolvedValue({}),
  wikiTemplateInstantiate: vi.fn().mockResolvedValue({ InstanceMap: { Id: 'inst::1' }, NodeMap: [] }),
  wikiListTemplateRuns: vi.fn().mockResolvedValue({ Runs: [] }),
  wikiValidateCard: vi.fn().mockResolvedValue({ Valid: true, Errors: [] }),
  graphGet: vi.fn().mockResolvedValue({ Meta: {}, EnvelopeText: '' }),
  OnCardChanged: vi.fn().mockReturnValue(() => {}),
  OnFileChanged: vi.fn().mockReturnValue(() => {}),
  OnGraphChanged: vi.fn().mockReturnValue(() => {}),
}))

vi.mock('../../gen-clients/workspace/client', () => ({
  wikiGetCard: vi.fn().mockResolvedValue({ Raw: '' }),
  wikiEditCard: vi.fn().mockResolvedValue({ Card: {} }),
  wikiDeleteCard: vi.fn().mockResolvedValue({}),
}))

vi.mock('../../gen-clients/projections', () => ({
  watchProjection: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('../ai/components/workflowFoldStore', () => {
  const snapshot = { loaded: true, foldedWorkflows: new Set(), foldedCategories: new Set(), expandedBuckets: new Set() }
  return {
    workflowFoldStore: {
      ensureLoaded: vi.fn().mockResolvedValue(undefined),
      getSnapshot: () => snapshot,
      subscribe: vi.fn().mockReturnValue(() => {}),
    },
    buildWorkflowFilter: vi.fn().mockReturnValue({
      FoldedWorkflows: [],
      FoldedCategories: [],
      ExpandedBuckets: [],
      AgentIds: ['agent-1'],
    }),
  }
})

vi.mock('../ai/hooks/agentListStore', () => ({
  getAgentListSnapshot: vi.fn().mockReturnValue({ version: 0, full: true, items: [{ ActorId: 'agent-1' }] }),
  prefetchAgentList: vi.fn().mockResolvedValue(undefined),
}))

import * as wikiClient from '../../gen-clients/project/client'
import * as projectClient from '../../gen-clients/project/client'
import * as workspaceWikiClient from '../../gen-clients/workspace/client'

describe('cardIdFromTitle', () => {
  it('slugs simple titles', () => {
    expect(cardIdFromTitle('Hello World')).toBe('hello-world')
  })

  it('handles Chinese characters', () => {
    expect(cardIdFromTitle('我的笔记')).toBe('我的笔记')
  })

  it('trims and lowercases', () => {
    expect(cardIdFromTitle('My COOL Project')).toBe('my-cool-project')
  })

  it('returns untitled for empty input', () => {
    expect(cardIdFromTitle('')).toBe('untitled')
    expect(cardIdFromTitle('   !!!   ')).toBe('untitled')
  })

  it('truncates long titles', () => {
    const long = 'A'.repeat(100)
    const result = cardIdFromTitle(long)
    expect(result.length).toBe(60)
  })

  it('handles special characters', () => {
    expect(cardIdFromTitle('Fix: bug #123!')).toBe('fix-bug-123')
  })
})

describe('monoStore draft filtering', () => {
  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('test-project')
    vi.clearAllMocks()
  })

  it('saveOpenCards strips draft IDs before persisting', async () => {
    await monoStore.saveOpenCards(['card-a', 'draft-123', 'card-b', 'draft-456'])
    expect(wikiClient.wikiSaveOpenCards).toHaveBeenCalledWith(
      expect.anything(),
      { OpenCards: ['card-a', 'card-b'] },
      expect.anything(),
    )
    expect(monoStore.getState().openCards).toEqual(['card-a', 'card-b'])
  })

  it('loadOpenCards filters out draft IDs from the server', async () => {
    ;(wikiClient.wikiGetOpenCards as any).mockResolvedValueOnce({ OpenCards: ['draft-old', 'card-a', 'draft-ancient'] })
    const openCards = await monoStore.loadOpenCards()
    expect(openCards).toEqual(['card-a'])
    expect(monoStore.getState().openCards).toEqual(['card-a'])
  })

  it('openCardAtTop ignores draft IDs', async () => {
    await monoStore.saveOpenCards(['card-a'])
    vi.clearAllMocks()
    await monoStore.openCardAtTop('draft-new')
    expect(wikiClient.wikiSaveOpenCards).not.toHaveBeenCalled()
    expect(monoStore.getState().openCards).toEqual(['card-a'])
  })

  it('closeCard removes real cards but ignores draft IDs', async () => {
    await monoStore.saveOpenCards(['card-a', 'card-b'])
    vi.clearAllMocks()
    await monoStore.closeCard('draft-xyz')
    expect(wikiClient.wikiSaveOpenCards).not.toHaveBeenCalled()
    expect(monoStore.getState().openCards).toEqual(['card-a', 'card-b'])
  })

  it('watchCardChanges refreshes the changed card and updates state', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnCardChanged as any).mockImplementation((_client: unknown, _actorId: string, cb: (payload: { Id: string; Modified: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [{ Id: 'card-a', Tags: [], List: [], Modified: 'old', Raw: '' }],
    })
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: A Updated\nmodified: new\n---\nbody',
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchCardChanges('test-project', controller.signal)
    expect(projectClient.OnCardChanged).toHaveBeenCalledWith(expect.anything(), 'test-project', expect.any(Function))
    handler({ Id: 'card-a', Modified: 'new' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.find(c => c.id === 'A Updated')?.id).toBe('A Updated')
    expect(state.cardRefreshKey['card-a']).toBe(1)
    controller.abort()
    expect(cancel).toHaveBeenCalled()
  })

  it('watchFileChanges refreshes only wiki card files and ignores other paths', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_client: unknown, _actorId: string, cb: (payload: { Path: string; Kind: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [{ Id: 'card-a', Tags: [], List: [], Modified: 'old', Raw: '' }],
    })
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: A Updated\nmodified: new\n---\nbody',
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchFileChanges('test-project', controller.signal)
    expect(projectClient.OnFileChanged).toHaveBeenCalledWith(expect.anything(), 'test-project', expect.any(Function))
    handler({ Path: 'src/main.go', Kind: 'write' })
    handler({ Path: '.sporecode/wiki/card-a.md', Kind: 'write' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.find(c => c.id === 'A Updated')?.id).toBe('A Updated')
    expect(state.cardRefreshKey['card-a']).toBe(1)
    expect(wikiClient.wikiGetCard).toHaveBeenCalledTimes(1)
    controller.abort()
    expect(cancel).toHaveBeenCalled()
  })

  it('cardRefreshKey bumps are throttled per card during file-watcher storms', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_client: unknown, _actorId: string, cb: (payload: { Path: string; Kind: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({ Cards: [] })
    ;(wikiClient.wikiGetCard as any).mockResolvedValue({
      Raw: '---\nid: Storm Card\nmodified: new\n---\nbody',
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchFileChanges('test-project', controller.signal)
    // Three flush cycles inside one 500ms throttle window: only the first
    // bumps immediately; data refetches stay unthrottled; one trailing bump
    // lands the final state.
    for (let i = 0; i < 3; i++) {
      handler({ Path: '.sporecode/wiki/storm-card.md', Kind: 'write' })
      await new Promise(resolve => setTimeout(resolve, 150))
    }
    expect(monoStore.getState().cardRefreshKey['storm-card']).toBe(1)
    expect(wikiClient.wikiGetCard).toHaveBeenCalledTimes(3)
    await new Promise(resolve => setTimeout(resolve, 600))
    expect(monoStore.getState().cardRefreshKey['storm-card']).toBe(2)
    controller.abort()
  })

  it('watchFileChanges adds an externally-created card not yet in the list', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_client: unknown, _actorId: string, cb: (payload: { Path: string; Kind: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({ Cards: [] })
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: External Card\nmodified: now\n---\nbody',
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchFileChanges('test-project', controller.signal)
    handler({ Path: '.sporecode/wiki/external-card.md', Kind: 'create' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.find(c => c.id === 'External Card')?.id).toBe('External Card')
    controller.abort()
  })

  it('watchFileChanges removes a card on delete/rename events', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_client: unknown, _actorId: string, cb: (payload: { Path: string; Kind: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [{ Id: 'doomed', Tags: [], List: [], Modified: 'old', Raw: '' }],
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchFileChanges('test-project', controller.signal)
    handler({ Path: '.sporecode/wiki/doomed.md', Kind: 'remove' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.find(c => c.id === 'Doomed')).toBeUndefined()
    controller.abort()
  })

  it('watchCardChanges never duplicates a card id on repeated identical change events', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnCardChanged as any).mockImplementation((_client: unknown, _actorId: string, cb: (payload: { Id: string; Modified: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({ Cards: [] })
    ;(wikiClient.wikiGetCard as any).mockResolvedValue({
      Raw: '---\nid: Dup Id\nmodified: now\n---\nbody',
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchCardChanges('test-project', controller.signal)
    handler({ Id: 'dup', Modified: 't1' })
    handler({ Id: 'dup', Modified: 't2' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.filter(c => c.id === 'Dup Id')).toHaveLength(1)
    controller.abort()
  })

  // Regression for the topology graph crash "Cannot add item: item with id
  // <title> already exists". When the file slug differs from the card's
  // frontmatter `id:` title (e.g. file `coordinator-落地页` vs frontmatter
  // `Coordinator 落地页（特殊 ProjectNewChat 变体）`), the two watchers keyed their
  // dedup check on the slug but the list item id came from the frontmatter,
  // so both watchers saw "not present" and each prepended a copy — duplicating
  // the id in monoState.cards, which crashes the vis DataSet on setData.
  it('card-changed + file-changed for a slug/title mismatch never duplicate the list id', async () => {
    const cardHandler = vi.fn()
    const cardCancel = vi.fn()
    ;(projectClient.OnCardChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { Id: string; Modified: string }) => void) => {
      cardHandler.mockImplementation(cb)
      return cardCancel
    })
    const fileHandler = vi.fn()
    const fileCancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { Path: string; Kind: string }) => void) => {
      fileHandler.mockImplementation(cb)
      return fileCancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({ Cards: [] })
    ;(wikiClient.wikiGetCard as any).mockResolvedValue({
      Raw: '---\nid: Coordinator 落地页（特殊 ProjectNewChat 变体）\nmodified: now\n---\nbody',
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchCardChanges('test-project', controller.signal)
    monoStore.watchFileChanges('test-project', controller.signal)
    cardHandler({ Id: 'coordinator-落地页', Modified: 'now' })
    fileHandler({ Path: '.sporecode/wiki/coordinator-落地页.md', Kind: 'write' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.filter(c => c.id === 'Coordinator 落地页（特殊 ProjectNewChat 变体）')).toHaveLength(1)
    controller.abort()
  })
})

describe('monoStore lightweight list mode', () => {
  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('test-project')
    vi.clearAllMocks()
  })

  it('load passes IncludeRaw:false and the fold filter to wikiListCards', async () => {
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({ Cards: [] })
    await monoStore.load()
    expect(wikiClient.wikiListCards).toHaveBeenCalledWith(
      expect.anything(),
      {
        Flat: true,
        IncludeRaw: false,
        IncludeBuiltin: true,
        Limit: -1,
        WorkflowFilter: {
          FoldedWorkflows: [],
          FoldedCategories: [],
          ExpandedBuckets: [],
          AgentIds: ['agent-1'],
        },
      },
      expect.anything(),
    )
  })

  it('fetchCardList uses lightweight list mode', async () => {
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({ Cards: [] })
    await monoStore.fetchCardList()
    expect(wikiClient.wikiListCards).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Flat: true, IncludeRaw: false, Limit: -1 }),
      expect.anything(),
    )
  })
})

describe('monoStore graph cache', () => {
  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('test-project')
    vi.clearAllMocks()
  })

  const envelope = (text: string) => ({ Meta: { GraphKind: 'workflow_topo', Id: 'map-1', Revision: 'rev-1' }, EnvelopeText: text })

  it('getGraph is cache-first: a second call does not refetch', async () => {
    ;(wikiClient.graphGet as any).mockResolvedValue(envelope('{"nodes":[],"edges":[]}'))
    const first = await monoStore.getGraph('workflow_topo', 'map-1')
    expect(first?.EnvelopeText).toBe('{"nodes":[],"edges":[]}')
    expect(wikiClient.graphGet).toHaveBeenCalledTimes(1)
    const second = await monoStore.getGraph('workflow_topo', 'map-1')
    expect(second).toBe(first)
    expect(wikiClient.graphGet).toHaveBeenCalledTimes(1)
  })

  it('getGraph returns null and does not throw when the graph is missing', async () => {
    ;(wikiClient.graphGet as any).mockRejectedValue(new Error('not found'))
    await expect(monoStore.getGraph('workflow_topo', 'missing')).resolves.toBeNull()
  })

  it('watchGraphChanges invalidates the cached graph and re-pulls it on graph_changed', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnGraphChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { GraphKind: string; Id: string; Revision: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.graphGet as any)
      .mockResolvedValueOnce(envelope('{"nodes":[],"edges":[]}'))
      .mockResolvedValueOnce({ Meta: { GraphKind: 'workflow_topo', Id: 'map-1', Revision: 'rev-2' }, EnvelopeText: '{"nodes":[{"id":"t1"}],"edges":[{"from":"t1","to":"t0","kind":"depends_on"}]}' })
    await monoStore.getGraph('workflow_topo', 'map-1')
    const controller = new AbortController()
    monoStore.watchGraphChanges('test-project', controller.signal)
    expect(projectClient.OnGraphChanged).toHaveBeenCalledWith(expect.anything(), 'test-project', expect.any(Function))
    handler({ GraphKind: 'workflow_topo', Id: 'map-1', Revision: 'rev-2' })
    await new Promise(resolve => setTimeout(resolve, 150))
    expect(monoStore.getState().graphRefreshKey['workflow_topo/map-1']).toBe(1)
    expect(wikiClient.graphGet).toHaveBeenCalledTimes(2)
    const fresh = await monoStore.getGraph('workflow_topo', 'map-1')
    expect(fresh?.Meta.Revision).toBe('rev-2')
    controller.abort()
    expect(cancel).toHaveBeenCalled()
  })

  it('watchGraphChanges preserves the cache when event revision matches the cached one', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnGraphChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { GraphKind: string; Id: string; Revision: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.graphGet as any).mockResolvedValueOnce(envelope('{"nodes":[],"edges":[]}'))
    await monoStore.getGraph('workflow_topo', 'map-1')
    const controller = new AbortController()
    monoStore.watchGraphChanges('test-project', controller.signal)
    handler({ GraphKind: 'workflow_topo', Id: 'map-1', Revision: 'rev-1' })
    await new Promise(resolve => setTimeout(resolve, 150))
    expect(monoStore.getState().graphRefreshKey['workflow_topo/map-1']).toBe(1)
    expect(wikiClient.graphGet).toHaveBeenCalledTimes(1)
    const cached = await monoStore.getGraph('workflow_topo', 'map-1')
    expect(cached?.Meta.Revision).toBe('rev-1')
    controller.abort()
    expect(cancel).toHaveBeenCalled()
  })

  it('watchGraphChanges falls back to a full refresh when the event revision is missing', async () => {
    const handler = vi.fn()
    const cancel = vi.fn()
    ;(projectClient.OnGraphChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { GraphKind: string; Id: string; Revision?: string }) => void) => {
      handler.mockImplementation(cb)
      return cancel
    })
    ;(wikiClient.graphGet as any)
      .mockResolvedValueOnce(envelope('{"nodes":[],"edges":[]}'))
      .mockResolvedValueOnce({ Meta: { GraphKind: 'workflow_topo', Id: 'map-1', Revision: 'rev-2' }, EnvelopeText: '{"nodes":[{"id":"t1"}],"edges":[]}' })
    await monoStore.getGraph('workflow_topo', 'map-1')
    const controller = new AbortController()
    monoStore.watchGraphChanges('test-project', controller.signal)
    handler({ GraphKind: 'workflow_topo', Id: 'map-1' })
    await new Promise(resolve => setTimeout(resolve, 150))
    expect(monoStore.getState().graphRefreshKey['workflow_topo/map-1']).toBe(1)
    expect(wikiClient.graphGet).toHaveBeenCalledTimes(2)
    const fresh = await monoStore.getGraph('workflow_topo', 'map-1')
    expect(fresh?.Meta.Revision).toBe('rev-2')
    controller.abort()
    expect(cancel).toHaveBeenCalled()
  })

  it('watchGraphChanges bumps the refresh key without fetching for an uncached graph', async () => {
    const handler = vi.fn()
    ;(projectClient.OnGraphChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { GraphKind: string; Id: string; Revision: string }) => void) => {
      handler.mockImplementation(cb)
      return () => {}
    })
    const controller = new AbortController()
    monoStore.watchGraphChanges('test-project', controller.signal)
    handler({ GraphKind: 'workflow_topo', Id: 'never-loaded', Revision: 'rev-9' })
    await new Promise(resolve => setTimeout(resolve, 150))
    expect(monoStore.getState().graphRefreshKey['workflow_topo/never-loaded']).toBe(1)
    expect(wikiClient.graphGet).not.toHaveBeenCalled()
    controller.abort()
  })

  it('ignores events with missing GraphKind or Id payloads', async () => {
    const handler = vi.fn()
    ;(projectClient.OnGraphChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: Partial<{ GraphKind: string; Id: string; Revision: string }>) => void) => {
      handler.mockImplementation(cb)
      return () => {}
    })
    const controller = new AbortController()
    monoStore.watchGraphChanges('test-project', controller.signal)
    handler({ GraphKind: '', Id: 'map-1', Revision: 'rev-1' })
    handler({ GraphKind: 'workflow_topo', Id: '', Revision: 'rev-1' })
    await new Promise(resolve => setTimeout(resolve, 150))
    expect(monoStore.getState().graphRefreshKey).toEqual({})
    controller.abort()
  })

  it('setProjectId clears the graph cache', async () => {
    ;(wikiClient.graphGet as any).mockResolvedValue(envelope('{"nodes":[],"edges":[]}'))
    await monoStore.getGraph('workflow_topo', 'map-1')
    monoStore.setProjectId(null)
    monoStore.setProjectId('other-project')
    vi.clearAllMocks()
    ;(wikiClient.graphGet as any).mockResolvedValue({ Meta: { Revision: 'rev-other' }, EnvelopeText: 'fresh' })
    const fresh = await monoStore.getGraph('workflow_topo', 'map-1')
    // The pre-switch cached envelope must not survive the project change.
    expect(fresh?.EnvelopeText).toBe('fresh')
    expect(wikiClient.graphGet).toHaveBeenCalledTimes(1)
    expect(monoStore.getState().graphRefreshKey).toEqual({})
  })
})

describe('monoStore status normalization', () => {
  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('test-project')
    vi.clearAllMocks()
  })

  it('createCard normalizes status aliases to canonical', async () => {
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: {
        Id: 'task',
        Tags: [],
        List: [],
        Modified: 'now',
        Raw: '---\nid: Task\nmodified: now\nstatus: doing\n---\n',
      },
    })
    await monoStore.createCard({ name: 'Task', body: '', tags: ['kanban-task'], status: 'in_progress' })
    const call = (wikiClient.wikiCreateCard as any).mock.calls[0]
    expect(call[1].Raw).toContain('status: doing')
  })

  it('updateCard normalizes status aliases to canonical', async () => {
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: Old\nmodified: old\n---\nbody',
    })
    ;(wikiClient.wikiEditCard as any).mockResolvedValueOnce({
      Card: {
        Id: 'old',
        Tags: [],
        List: [],
        Modified: 'new',
        Raw: '---\nid: Old\nmodified: new\nstatus: doing\n---\nbody',
      },
    })
    await monoStore.updateCard('old', { status: 'in_progress' })
    const call = (wikiClient.wikiEditCard as any).mock.calls[0]
    expect(call[1].Raw).toContain('status: doing')
  })


  it('respects custom aliases from a task-status-map card', async () => {
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [
        {
          Id: 'map',
          Tags: ['__builtin_task_status_map__'],
          List: [],
          Modified: 'now',
          Raw: '',
          Data: { aliases: ['in_progress:blocked'] },
        },
      ],
    })
    await monoStore.load()
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: {
        Id: 'task2',
        Tags: [],
        List: [],
        Modified: 'now',
        Raw: '',
      },
    })
    await monoStore.createCard({ name: 'Task2', body: '', status: 'in_progress' })
    const call = (wikiClient.wikiCreateCard as any).mock.calls[0]
    expect(call[1].Raw).toContain('status: blocked')
  })

  it('createCard stores title from name and writes id frontmatter for all cards', async () => {
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: {
        Id: 'hello-world',
        Tags: [],
        List: [],
        Modified: 'now',
        Raw: '',
      },
    })
    await monoStore.createCard({ name: 'Hello World', body: 'body', tags: [] })
    const call = (wikiClient.wikiCreateCard as any).mock.calls[0]
    expect(call[1].Id).toBe('hello-world')
    expect(call[1].Raw).toContain('id: Hello World')
    expect(call[1].Raw).toContain('tags: []')
  })

  it('updateCard preserves an independent title for non-system cards', async () => {
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: Old\nmodified: old\n---\nbody',
    })
    ;(wikiClient.wikiEditCard as any).mockResolvedValueOnce({
      Card: {
        Id: 'old',
        Tags: [],
        List: [],
        Modified: 'new',
        Raw: '',
      },
    })
    await monoStore.updateCard('old', { id: 'New Title' })
    const call = (wikiClient.wikiEditCard as any).mock.calls[0]
    expect(call[1].Raw).toContain('id: New Title')
  })

  it('createCard does not duplicate when a card with the same id already exists', async () => {
    const card = { Id: 'dup', Tags: [], List: [], Modified: 'now', Raw: '' }
    ;(wikiClient.wikiCreateCard as any).mockResolvedValue({ Card: card })
    await monoStore.createCard({ name: 'dup', body: '' })
    await monoStore.createCard({ name: 'dup', body: '' })
    const ids = monoStore.getState().cards.map(c => c.id)
    expect(ids.filter(id => id === 'dup')).toHaveLength(1)
  })

  it('upsertListItem collapses pre-existing duplicates to a single entry', async () => {
    // Simulate a stale state where two entries share one id.
    const dup = { id: 'dup', type: 'wiki', tags: [], list: [], modified: 'old', created: 'old', parent: '', data: {}, raw: '', standalone: false, visibility: '', storage: '' } as any
    ;(monoStore as any).setState({ cards: [dup, { ...dup }] })
    // Trigger upsert via a watcher-style update (watchCardChanges calls upsertListItem).
    const updated = { Id: 'dup', Tags: [], List: [], Modified: 'new', Raw: '' }
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({ Raw: '---\nid: dup\nmodified: new\n---\n' })
    ;(wikiClient.wikiEditCard as any).mockResolvedValueOnce({ Card: updated })
    await monoStore.updateCard('dup', { body: 'updated' })
    const ids = monoStore.getState().cards.map(c => c.id)
    expect(ids.filter(id => id === 'dup')).toHaveLength(1)
  })
})

describe('monoStore workspace fallback for system cards', () => {
  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('test-project')
    vi.clearAllMocks()
  })

  it('getCard falls back to workspace store for skill: cards not in project store', async () => {
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: skill:my-skill\ntype: skill\n---\nbody',
    })
    const card = await monoStore.getCard('skill:my-skill')
    expect(card).not.toBeNull()
    expect(card!.id).toBe('skill:my-skill')
    expect(workspaceWikiClient.wikiGetCard).toHaveBeenCalledWith(expect.anything(), { Id: 'skill:my-skill' })
  })

  it('getCard falls back to workspace store for prompt: cards', async () => {
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: prompt:profile:project.coder\ntype: prompt\n---\nrole body',
    })
    const card = await monoStore.getCard('prompt:profile:project.coder')
    expect(card).not.toBeNull()
    expect(card!.id).toBe('prompt:profile:project.coder')
  })

  it('getCard falls back to workspace store for builtin: cards', async () => {
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: builtin:bundle:file-tools\n---\ntools',
    })
    const card = await monoStore.getCard('builtin:bundle:file-tools')
    expect(card).not.toBeNull()
  })

  it('getCard returns null when card is not in either store', async () => {
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    const card = await monoStore.getCard('my-note')
    expect(card).toBeNull()
  })

  it('getCard falls back to workspace for legacy unprefixed display-name cards', async () => {
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\ntitle: Coder\ntags: [component, prompt, internal]\n---\ncoder body',
    })
    const card = await monoStore.getCard('Coder')
    expect(card).not.toBeNull()
    expect(workspaceWikiClient.wikiGetCard).toHaveBeenCalledWith(expect.anything(), { Id: 'Coder' })
  })

  it('updateCard routes system card edits to workspace store', async () => {
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\nid: skill:my-skill\ntype: skill\n---\nold body',
    })
    ;(workspaceWikiClient.wikiEditCard as any).mockResolvedValueOnce({
      Card: { Id: 'skill:my-skill', Tags: [], List: [], Modified: 'now', Raw: '' },
    })
    await monoStore.updateCard('skill:my-skill', { body: 'new body' })
    expect(workspaceWikiClient.wikiEditCard).toHaveBeenCalled()
    expect(wikiClient.wikiEditCard).not.toHaveBeenCalled()
  })

  it('validateCard uses frontend validation for system cards', async () => {
    const raw = '---\nid: skill:test\ntype: skill\ntags: []\ncreated: "2025-01-01"\nmodified: "2025-01-01"\n---\nbody'
    const resp = await monoStore.validateCard('skill:test', raw)
    expect(resp.Valid).toBe(true)
    expect(wikiClient.wikiValidateCard).not.toHaveBeenCalled()
  })

  it('updateCard routes workspace-loaded unprefixed cards to workspace store', async () => {
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockResolvedValueOnce({
      Raw: '---\ntitle: Coder\ntags: [component, prompt, internal]\n---\nold body',
    })
    ;(workspaceWikiClient.wikiEditCard as any).mockResolvedValueOnce({
      Card: { Id: 'Coder', Tags: [], List: [], Modified: 'now', Raw: '' },
    })
    await monoStore.updateCard('Coder', { body: 'new body' })
    expect(workspaceWikiClient.wikiEditCard).toHaveBeenCalled()
    expect(wikiClient.wikiEditCard).not.toHaveBeenCalled()
  })
})

describe('monoStore card change batching', () => {
  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('test-project')
    vi.clearAllMocks()
  })

  it('batches multiple card-changed events inside the flush window into one setState and Promise.all', async () => {
    const cardHandler = vi.fn()
    const cardCancel = vi.fn()
    ;(projectClient.OnCardChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { Id: string; Modified: string }) => void) => {
      cardHandler.mockImplementation(cb)
      return cardCancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [{ Id: 'card-a', Tags: [], List: [], Modified: 'old', Raw: '' }],
    })
    ;(wikiClient.wikiGetCard as any)
      .mockResolvedValueOnce({ Raw: '---\nid: card-a\nmodified: t2\n---\nbody' })
      .mockResolvedValueOnce({ Raw: '---\nid: card-b\nmodified: t2\n---\nbody' })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchCardChanges('test-project', controller.signal)
    cardHandler({ Id: 'card-a', Modified: 't1' })
    cardHandler({ Id: 'card-b', Modified: 't2' })
    cardHandler({ Id: 'card-a', Modified: 't3' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.find(c => c.id === 'card-a')).toBeDefined()
    expect(state.cards.find(c => c.id === 'card-b')).toBeDefined()
    expect(state.cards.filter(c => c.id === 'card-a')).toHaveLength(1)
    expect(wikiClient.wikiGetCard).toHaveBeenCalledTimes(2)
    expect(state.cardRefreshKey['card-a']).toBe(1)
    expect(state.cardRefreshKey['card-b']).toBe(1)
    controller.abort()
  })

  it('dedupes card-changed and file-changed for the same id into a single getCard', async () => {
    const cardHandler = vi.fn()
    const cardCancel = vi.fn()
    ;(projectClient.OnCardChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { Id: string; Modified: string }) => void) => {
      cardHandler.mockImplementation(cb)
      return cardCancel
    })
    const fileHandler = vi.fn()
    const fileCancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { Path: string; Kind: string }) => void) => {
      fileHandler.mockImplementation(cb)
      return fileCancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({ Cards: [] })
    ;(wikiClient.wikiGetCard as any).mockResolvedValue({
      Raw: '---\nid: same-card\nmodified: now\n---\nbody',
    })
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchCardChanges('test-project', controller.signal)
    monoStore.watchFileChanges('test-project', controller.signal)
    cardHandler({ Id: 'same-card', Modified: 't1' })
    fileHandler({ Path: '.sporecode/wiki/same-card.md', Kind: 'write' })
    cardHandler({ Id: 'same-card', Modified: 't2' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.filter(c => c.id === 'same-card')).toHaveLength(1)
    expect(wikiClient.wikiGetCard).toHaveBeenCalledTimes(1)
    expect(state.cardRefreshKey['same-card']).toBe(1)
    controller.abort()
  })

  it('merges remove/rename events from file-changed with upserts in the same batch window', async () => {
    const fileHandler = vi.fn()
    const fileCancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { Path: string; Kind: string }) => void) => {
      fileHandler.mockImplementation(cb)
      return fileCancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [
        { Id: 'keep', Tags: [], List: [], Modified: 'old', Raw: '' },
        { Id: 'gone', Tags: [], List: [], Modified: 'old', Raw: '' },
      ],
    })
    // Keyed by request so leftover mockResolvedValueOnce queues from earlier
    // tests cannot leak into this batch.
    ;(wikiClient.wikiGetCard as any).mockImplementation((_c: unknown, req: { Id: string }) =>
      Promise.resolve(req.Id === 'keep'
        ? { Raw: '---\nid: keep\nmodified: now\n---\nbody' }
        : { Raw: '' }),
    )
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchFileChanges('test-project', controller.signal)
    fileHandler({ Path: '.sporecode/wiki/keep.md', Kind: 'write' })
    fileHandler({ Path: '.sporecode/wiki/gone.md', Kind: 'remove' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.find(c => c.id === 'keep')).toBeDefined()
    expect(state.cards.find(c => c.id === 'gone')).toBeUndefined()
    expect(state.cards).toHaveLength(1)
    controller.abort()
  })

  it('keeps a card whose watcher reports remove/rename while the card still exists (atomic-save rename-replace)', async () => {
    const fileHandler = vi.fn()
    const fileCancel = vi.fn()
    ;(projectClient.OnFileChanged as any).mockImplementation((_c: unknown, _a: string, cb: (p: { Path: string; Kind: string }) => void) => {
      fileHandler.mockImplementation(cb)
      return fileCancel
    })
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [{ Id: 'active-map', Tags: [], List: [], Modified: 'old', Raw: '' }],
    })
    ;(wikiClient.wikiGetCard as any).mockImplementation((_c: unknown, req: { Id: string }) =>
      Promise.resolve(req.Id === 'active-map'
        ? { Raw: '---\nid: active-map\nmodified: now\n---\nbody' }
        : { Raw: '' }),
    )
    await monoStore.load()
    const controller = new AbortController()
    monoStore.watchFileChanges('test-project', controller.signal)
    // An atomic save lands as temp+rename; the watcher surfaces a rename for
    // the map card even though it still exists.
    fileHandler({ Path: '.sporecode/wiki/active-map.md', Kind: 'rename' })
    await new Promise(resolve => setTimeout(resolve, 150))
    const state = monoStore.getState()
    expect(state.cards.find(c => c.id === 'active-map')).toBeDefined()
    expect(state.cards).toHaveLength(1)
    controller.abort()
  })
})

describe('monoStore template instantiation & runs (P1/U1 + N1)', () => {
  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('test-project')
    vi.clearAllMocks()
  })

  it('instantiateWorkflow calls wikiTemplateInstantiate with a backend-convention instance id and Source=manual', async () => {
    ;(wikiClient.wikiTemplateInstantiate as any).mockResolvedValueOnce({
      InstanceMap: { Id: 'inst::20260823120000::tpl::MyFlow', Tags: ['workflow'], List: [], Modified: 'now' },
      NodeMap: [],
    })
    const instance = await monoStore.instantiateWorkflow('tpl::MyFlow')
    expect(instance?.id).toBe('inst::20260823120000::tpl::MyFlow')
    expect(wikiClient.wikiTemplateInstantiate).toHaveBeenCalledTimes(1)
    const [calledClient, req] = (wikiClient.wikiTemplateInstantiate as any).mock.calls[0]
    expect(calledClient).toBeDefined()
    // InstanceMapId follows the backend convention inst::<UTC ts yyyyMMddHHmmss>::<templateId>.
    expect(req.InstanceMapId).toMatch(/^inst::\d{14}::tpl::MyFlow$/)
    expect(req.TemplateMapId).toBe('tpl::MyFlow')
    expect(req.Source).toBe('manual')
    expect(req.SchedulerCardId).toBeUndefined()
    // The new instance map is upserted into the card list.
    expect(monoStore.getState().cards.find(c => c.id === 'inst::20260823120000::tpl::MyFlow')?.id).toBe('inst::20260823120000::tpl::MyFlow')
  })

  it('instantiateWorkflow returns null without a project id', async () => {
    monoStore.setProjectId(null)
    await expect(monoStore.instantiateWorkflow('tpl::MyFlow')).resolves.toBeNull()
    expect(wikiClient.wikiTemplateInstantiate).not.toHaveBeenCalled()
  })

  it('listTemplateRuns passes TemplateMapId and Limit and returns the runs', async () => {
    ;(wikiClient.wikiListTemplateRuns as any).mockResolvedValueOnce({
      Runs: [
        { InstanceMapId: 'inst::2', StartedAt: 't2' },
        { InstanceMapId: 'inst::1', StartedAt: 't1' },
      ],
    })
    const runs = await monoStore.listTemplateRuns('tpl::MyFlow', 5)
    expect(wikiClient.wikiListTemplateRuns).toHaveBeenCalledTimes(1)
    const [, req] = (wikiClient.wikiListTemplateRuns as any).mock.calls[0]
    expect(req.TemplateMapId).toBe('tpl::MyFlow')
    expect(req.Limit).toBe(5)
    expect(runs.map(r => r.InstanceMapId)).toEqual(['inst::2', 'inst::1'])
  })

  it('listTemplateRuns falls back to an empty array', async () => {
    const runs = await monoStore.listTemplateRuns('tpl::MyFlow')
    expect(runs).toEqual([])
  })

  it('createSchedulerCard sends a scheduler-prefixed card bound to the template', async () => {
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: { Id: 'scheduler:MyFlow', Tags: ['scheduler'], List: [], Modified: 'now' },
    })
    const card = await monoStore.createSchedulerCard('tpl::MyFlow')
    expect(card?.id).toBe('scheduler:MyFlow')
    expect(wikiClient.wikiCreateCard).toHaveBeenCalledTimes(1)
    const [, req] = (wikiClient.wikiCreateCard as any).mock.calls[0]
    // Id must carry the backend scheduler: prefix (auto-registers with the
    // scheduler service on create) and the raw must pin type/tags/cron, the
    // unified task type and the workflow_template so the card fires this
    // template. Template-bound tasks run by the template flow, so the body
    // (prompt) may stay empty.
    expect(req.Id).toBe('scheduler:MyFlow')
    expect(req.Raw).toContain('id: scheduler:MyFlow')
    expect(req.Raw).toContain('type: scheduler')
    expect(req.Raw).toContain('tags: [scheduler]')
    expect(req.Raw).toContain('schedule_type: task')
    expect(req.Raw).toContain('cron: "0 9 * * *"')
    expect(req.Raw).toContain('workflow_template: "tpl::MyFlow"')
    expect(req.Raw).not.toContain('executor')
    expect(monoStore.getState().cards.find(c => c.id === 'scheduler:MyFlow')).toBeDefined()
  })

  it('createSchedulerCard with null template creates a prompt-mode task', async () => {
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: { Id: 'scheduler:prompt-task', Tags: ['scheduler'], List: [], Modified: 'now' },
    })
    const card = await monoStore.createSchedulerCard(null)
    expect(card?.id).toBe('scheduler:prompt-task')
    const [, req] = (wikiClient.wikiCreateCard as any).mock.calls[0]
    // Prompt-mode task: schedule_type pins task, no workflow_template, and the
    // body carries the prompt placeholder (a prompt is required when no
    // template is bound).
    expect(req.Id).toBe('scheduler:prompt-task')
    expect(req.Raw).toContain('schedule_type: task')
    expect(req.Raw).not.toContain('workflow_template')
    expect(req.Raw).not.toContain('executor')
    expect(req.Raw).toContain('Describe what this scheduled task should do — this body is the prompt sent to the agent created for each fire.')
  })

  it('createSchedulerCard with task opts pins the created agent kind and model slot', async () => {
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: { Id: 'scheduler:prompt-task', Tags: ['scheduler'], List: [], Modified: 'now' },
    })
    const card = await monoStore.createSchedulerCard(null, {
      type: 'task',
      agentKind: 'dreamer',
      agentSlot: { Candidates: [{ kind: 'unit', Unit: { model: 'gpt-5', provider: 'openai' } }] },
    })
    expect(card?.id).toBe('scheduler:prompt-task')
    const [, req] = (wikiClient.wikiCreateCard as any).mock.calls[0]
    // The task's agent fields ride in the data block: agent_kind names the
    // kind spawned per fire and model_slots carries the model slot JSON under
    // its primary key.
    expect(req.Raw).toContain('schedule_type: task')
    expect(req.Raw).toContain('agent_kind: dreamer')
    expect(req.Raw).toContain(`model_slots: '{"primary":{"Candidates":[{"kind":"unit","Unit":{"model":"gpt-5","provider":"openai"}}]}}'`)
    expect(req.Raw).not.toContain('workflow_template')
  })

  it('createSchedulerCard with agent_action opts creates an agent-action scheduler', async () => {
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: { Id: 'scheduler:agent-action', Tags: ['scheduler'], List: [], Modified: 'now' },
    })
    const card = await monoStore.createSchedulerCard(null, { type: 'agent_action', agentAction: 'resume', targetAgent: 'agent:my-coder' })
    expect(card?.id).toBe('scheduler:agent-action')
    const [, req] = (wikiClient.wikiCreateCard as any).mock.calls[0]
    // Agent-action type is stored as a unified task with an agent_actions JSON
    // list; schedule_type is always 'task' and the legacy agent_action/target_agent
    // fields are not written.
    expect(req.Id).toBe('scheduler:agent-action')
    expect(req.Raw).toContain('schedule_type: task')
    expect(req.Raw).toContain('agent_actions:')
    expect(req.Raw).toContain('"action":"resume"')
    expect(req.Raw).toContain('"target":"agent:my-coder"')
    expect(req.Raw).not.toContain('agent_action: resume')
    expect(req.Raw).not.toContain('target_agent: "agent:my-coder"')
    expect(req.Raw).not.toContain('executor')
    expect(req.Raw).not.toContain('workflow_template')
  })

  it('createSchedulerCard dedupes against existing scheduler cards', async () => {
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [{ Id: 'scheduler:MyFlow', Type: 'scheduler', Tags: ['scheduler'], List: [], Modified: 'now' }],
    })
    await monoStore.load()
    ;(wikiClient.wikiCreateCard as any).mockResolvedValueOnce({
      Card: { Id: 'scheduler:MyFlow-2', Tags: ['scheduler'], List: [], Modified: 'now' },
    })
    const card = await monoStore.createSchedulerCard('tpl::MyFlow')
    expect(card?.id).toBe('scheduler:MyFlow-2')
    const [, req] = (wikiClient.wikiCreateCard as any).mock.calls[0]
    expect(req.Id).toBe('scheduler:MyFlow-2')
  })

  it('createSchedulerCard returns null without a project id', async () => {
    monoStore.setProjectId(null)
    await expect(monoStore.createSchedulerCard('tpl::MyFlow')).resolves.toBeNull()
    expect(wikiClient.wikiCreateCard).not.toHaveBeenCalled()
  })
})

describe('monoStore explicit-project operations (right-panel tabs across project switches)', () => {
  const RAW = '---\nid: foreign-card\ntype: wiki\ntags: []\n---\n\nbody'

  beforeEach(() => {
    monoStore.setProjectId(null)
    monoStore.setProjectId('active-project')
    vi.clearAllMocks()
  })

  it('getCard with a foreign projectId routes to that project target', async () => {
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({ Raw: RAW })
    const card = await monoStore.getCard('foreign-card', { projectId: 'other-project' })
    expect(card?.id).toBe('foreign-card')
    expect(wikiClient.wikiGetCard).toHaveBeenCalledWith(
      expect.anything(),
      { Id: 'foreign-card' },
      { target: 'other-project' },
    )
  })

  it('getCard with a foreign projectId does not serve or pollute the singleton cache', async () => {
    ;(wikiClient.wikiGetCard as any).mockResolvedValue({ Raw: RAW })
    await monoStore.getCard('foreign-card', { projectId: 'other-project' })
    // Second foreign fetch still hits the wire (no cross-project cache reuse).
    await monoStore.getCard('foreign-card', { projectId: 'other-project' })
    expect(wikiClient.wikiGetCard).toHaveBeenCalledTimes(2)
    // A later current-project fetch must not return the foreign card from cache.
    ;(wikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    ;(workspaceWikiClient.wikiGetCard as any).mockRejectedValueOnce(new Error('card not found'))
    const miss = await monoStore.getCard('foreign-card')
    expect(miss).toBeNull()
  })

  it('updateCard with a foreign projectId edits that project and skips singleton list state', async () => {
    ;(wikiClient.wikiGetCard as any).mockResolvedValueOnce({ Raw: RAW })
    ;(wikiClient.wikiEditCard as any).mockResolvedValueOnce({ Card: { Id: 'foreign-card', Tags: [], List: [], Modified: 'now' } })
    const before = monoStore.getState()
    const updated = await monoStore.updateCard('foreign-card', { body: 'new body' }, { projectId: 'other-project' })
    expect(updated?.body).toBe('new body')
    expect(wikiClient.wikiEditCard).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({ Id: 'foreign-card' }),
      { target: 'other-project' },
    )
    const after = monoStore.getState()
    expect(after.cards).toBe(before.cards)
    expect(after.cardRefreshKey['foreign-card']).toBeUndefined()
  })

  it('validateCard with a foreign projectId routes backend validation to that project', async () => {
    await monoStore.validateCard('foreign-card', RAW, { projectId: 'other-project' })
    expect(wikiClient.wikiValidateCard).toHaveBeenCalledWith(
      expect.anything(),
      { Id: 'foreign-card', Raw: RAW },
      { target: 'other-project' },
    )
  })

  it('deleteCard with a foreign projectId deletes in that project and keeps the singleton list intact', async () => {
    ;(wikiClient.wikiListCards as any).mockResolvedValueOnce({
      Cards: [{ Id: 'active-card', Type: 'wiki', Tags: [], List: [], Modified: 'now' }],
    })
    await monoStore.load()
    await monoStore.deleteCard('foreign-card', { projectId: 'other-project' })
    expect(wikiClient.wikiDeleteCard).toHaveBeenCalledWith(
      expect.anything(),
      { Id: 'foreign-card' },
      { target: 'other-project' },
    )
    expect(monoStore.getState().cards.some(c => c.id === 'active-card')).toBe(true)
  })

  it('closeCard with a foreign projectId closes via the single-card callable in that project', async () => {
    await monoStore.closeCard('foreign-card', { projectId: 'other-project' })
    expect(wikiClient.wikiCloseCard).toHaveBeenCalledWith(
      expect.anything(),
      { Id: 'foreign-card' },
      { target: 'other-project' },
    )
    expect(wikiClient.wikiSaveOpenCards).not.toHaveBeenCalled()
  })

  it('closeCard without a foreign project keeps full-list save semantics', async () => {
    await monoStore.closeCard('foreign-card')
    expect(wikiClient.wikiCloseCard).not.toHaveBeenCalled()
    expect(wikiClient.wikiSaveOpenCards).toHaveBeenCalled()
  })
})
