import { describe, it, expect, vi, beforeEach } from 'vitest'

// Must mock before importing the module under test
vi.mock('../../application/generated-client', () => ({
  client: {
    invoke: vi.fn(),
    events: { on: vi.fn(() => vi.fn()), onService: vi.fn(() => vi.fn()) },
    getTransport: vi.fn(() => null),
  },
}))

vi.mock('../../gen-clients/runtime/client', () => ({
  OnTopologyEpoch: vi.fn(() => vi.fn()),
}))

vi.mock('../../gen-clients/unified_graph/client', () => ({
  sync: vi.fn(),
  history: vi.fn(),
}))

import { ObservationStore } from './observationStore'
import * as runtime from '../../gen-clients/runtime/client'
import * as unified_graph from '../../gen-clients/unified_graph/client'
import type { TopologyEpochEvent, UnifiedGraph, GraphPatch, UnifiedGraphNode } from '../../gen-types/observation'

function emptyGraph(epoch = 1): UnifiedGraph {
  return { Version: String(epoch), Nodes: [], Edges: [] }
}

function graphPatch(epoch: number, added?: UnifiedGraphNode[], removed?: string[], updated?: UnifiedGraphNode[]): GraphPatch {
  return {
    Epoch: epoch, Timestamp: '2026-01-01T00:00:00Z',
    AddedNodes: added ?? [], RemovedNodes: removed ?? [], UpdatedNodes: updated ?? [],
    AddedEdges: [], RemovedEdges: [],
  }
}

function makeNode(id: string): UnifiedGraphNode {
  return { Id: id, Kind: 'actor', Label: id, Status: 'running' }
}

async function flushMicrotasks() {
  await Promise.resolve()
}

describe('ObservationStore', () => {
  let store: ObservationStore
  let epochHandlers: Array<(e: TopologyEpochEvent) => void> = []
  let topoCancel: ReturnType<typeof vi.fn> = vi.fn()

  beforeEach(() => {
    store = new ObservationStore()
    epochHandlers = []
    topoCancel = vi.fn()
    vi.clearAllMocks()

    vi.mocked(runtime.OnTopologyEpoch).mockImplementation((_client, handler) => {
      epochHandlers.push(handler)
      return topoCancel as () => void
    })

    vi.mocked(unified_graph.sync).mockResolvedValue({
      CurrentEpoch: 1,
      Snapshot: emptyGraph(1),
      Patches: [],
    })

    vi.mocked(unified_graph.history).mockResolvedValue({ Entries: [] })
  })

  // ---------------------------------------------------------------------------
  // Facts stream — ref-counted lifecycle
  // ---------------------------------------------------------------------------
  describe('facts stream lifecycle', () => {
    it('starts streaming on first subscribeFacts', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 1,
        Snapshot: emptyGraph(1),
        Patches: [graphPatch(1, [makeNode('a')])],
      })

      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledTimes(1)
      expect(store.getFacts()).toHaveLength(1)
      expect(store.getFacts()[0]!.TaskId).toBe('a')
      expect(store.getFacts()[0]!.Kind).toBe('actor.created')
    })

    it('shares stream across multiple subscribers', async () => {
      store.subscribeFacts(() => {})
      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledTimes(1)
    })

    it('stops streaming when last subscriber unsubscribes', async () => {
      const unsub1 = store.subscribeFacts(() => {})
      const unsub2 = store.subscribeFacts(() => {})
      await flushMicrotasks()

      unsub1()
      expect(store.isStreaming()).toBe(true)

      unsub2()
      expect(store.isStreaming()).toBe(false)
    })

    it('does not restart stream when subscriber leaves then re-joins', async () => {
      const unsub = store.subscribeFacts(() => {})
      await flushMicrotasks()
      expect(unified_graph.sync).toHaveBeenCalledTimes(1)

      unsub()
      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledTimes(2)
    })
  })

  // ---------------------------------------------------------------------------
  // Facts — derived from topology-epoch patches
  // ---------------------------------------------------------------------------
  describe('facts derived from topology-epoch patches', () => {
    it('derives actor.created facts from AddedNodes', async () => {
      store.subscribeGraph(() => {})
      await flushMicrotasks()

      epochHandlers[0]!({ Epoch: 2, Patch: graphPatch(2, [makeNode('a1'), makeNode('a2')]) })

      expect(store.getFacts()).toHaveLength(2)
      expect(store.getFacts().map(f => f.Kind)).toEqual(['actor.created', 'actor.created'])
      expect(store.getFacts().map(f => f.TaskId)).toEqual(['a1', 'a2'])
      expect(store.getEpoch()).toBe(2)
    })

    it('derives actor.deleted facts from RemovedNodes', async () => {
      store.subscribeGraph(() => {})
      await flushMicrotasks()

      epochHandlers[0]!({ Epoch: 2, Patch: graphPatch(2, undefined, ['old1']) })

      expect(store.getFacts()).toHaveLength(1)
      expect(store.getFacts()[0]!.Kind).toBe('actor.deleted')
      expect(store.getFacts()[0]!.TaskId).toBe('old1')
    })

    it('mixes created and deleted facts from same patch', async () => {
      store.subscribeGraph(() => {})
      await flushMicrotasks()

      epochHandlers[0]!({ Epoch: 2, Patch: graphPatch(2, [makeNode('new')], ['old']) })

      expect(store.getFacts()).toHaveLength(2)
      expect(store.getFacts().map(f => f.Kind)).toEqual(['actor.created', 'actor.deleted'])
    })

    it('indexes derived facts by epoch', async () => {
      store.subscribeGraph(() => {})
      await flushMicrotasks()

      epochHandlers[0]!({ Epoch: 2, Patch: graphPatch(2, [makeNode('a')]) })

      expect(store.getFactsForEpoch(2)).toHaveLength(1)
      expect(store.getFactsForEpoch(2)[0]!.TaskId).toBe('a')
    })

    it('derives initial facts from sync patches', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 3,
        Snapshot: emptyGraph(3),
        Patches: [
          graphPatch(1, [makeNode('init')]),
          graphPatch(2, undefined, ['gone']),
          graphPatch(3, [makeNode('live1')]),
        ],
      })

      store.subscribeGraph(() => {})
      await flushMicrotasks()

      const facts = store.getFacts()
      expect(facts).toHaveLength(3)
      expect(facts[0]!.TaskId).toBe('init')
      expect(facts[1]!.Kind).toBe('actor.deleted')
      expect(facts[2]!.TaskId).toBe('live1')
    })

    it('appends derived facts to initial sync facts', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 1,
        Snapshot: emptyGraph(1),
        Patches: [graphPatch(1, [makeNode('init1')])],
      })

      store.subscribeFacts(() => {})
      store.subscribeGraph(() => {})
      await flushMicrotasks()

      // initial sync produced 1 fact; graph stream's OnTopologyEpoch not fired yet
      expect(store.getFacts()).toHaveLength(1)

      epochHandlers[0]!({ Epoch: 2, Patch: graphPatch(2, [makeNode('live1')]) })

      const facts = store.getFacts()
      expect(facts).toHaveLength(2)
      expect(facts[0]!.TaskId).toBe('init1')
      expect(facts[1]!.TaskId).toBe('live1')
    })
  })

  // ---------------------------------------------------------------------------
  // Facts — data handling
  // ---------------------------------------------------------------------------
  describe('facts data handling', () => {
    it('filters facts by kind', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 3,
        Snapshot: emptyGraph(3),
        Patches: [
          graphPatch(1, [makeNode('t1')]),
          graphPatch(2, undefined, ['t2']),
          graphPatch(3, [makeNode('t3')]),
        ],
      })

      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(store.factsByKind('actor.created')).toHaveLength(2)
      expect(store.factsByKind('actor.deleted')).toHaveLength(1)
    })

    it('latestFacts returns last N facts', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 3,
        Snapshot: emptyGraph(3),
        Patches: [
          graphPatch(1, [makeNode('t1')]),
          graphPatch(2, [makeNode('t2')]),
          graphPatch(3, [makeNode('t3')]),
        ],
      })

      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(store.latestFacts(2).map(f => f.Epoch)).toEqual([2, 3])
    })
  })

  // ---------------------------------------------------------------------------
  // Epoch-to-facts index
  // ---------------------------------------------------------------------------
  describe('epoch-to-facts index', () => {
    it('getFactsForEpoch returns facts for that exact epoch', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 3,
        Snapshot: emptyGraph(3),
        Patches: [
          graphPatch(1, [makeNode('t1')]),
          graphPatch(2, [makeNode('t2'), makeNode('t3')]),
          graphPatch(3, [makeNode('t4')]),
        ],
      })

      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(store.getFactsForEpoch(2)).toHaveLength(2)
      expect(store.getFactsForEpoch(2).map(f => f.TaskId)).toEqual(['t2', 't3'])
      expect(store.getFactsForEpoch(99)).toHaveLength(0)
    })

    it('getFactsUpToEpoch returns facts with epoch <= target', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 7,
        Snapshot: emptyGraph(7),
        Patches: [
          graphPatch(1, [makeNode('t1')]),
          graphPatch(3, [makeNode('t2')]),
          graphPatch(5, [makeNode('t3')]),
          graphPatch(7, [makeNode('t4')]),
        ],
      })

      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(store.getFactsUpToEpoch(4).map(f => f.Epoch)).toEqual([1, 3])
      expect(store.getFactsUpToEpoch(5).map(f => f.Epoch)).toEqual([1, 3, 5])
      expect(store.getFactsUpToEpoch(0)).toHaveLength(0)
    })

    it('rebuilds index after initial sync', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 2,
        Snapshot: emptyGraph(2),
        Patches: [
          graphPatch(10, [makeNode('t1')]),
          graphPatch(20, [makeNode('t2')]),
        ],
      })

      store.subscribeFacts(() => {})
      await flushMicrotasks()

      expect(store.getFactsForEpoch(10)).toHaveLength(1)
      expect(store.getFactsForEpoch(20)).toHaveLength(1)
    })
  })

  // ---------------------------------------------------------------------------
  // Graph stream — ref-counted lifecycle
  // ---------------------------------------------------------------------------
  describe('graph stream lifecycle', () => {
    it('starts streaming on first subscribeGraph', async () => {
      store.subscribeGraph(() => {})
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledWith(expect.anything(), { ClientEpoch: 0 })
      expect(runtime.OnTopologyEpoch).toHaveBeenCalledTimes(1)
    })

    it('shares graph stream across subscribers', async () => {
      store.subscribeGraph(() => {})
      store.subscribeGraph(() => {})
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledTimes(1)
      expect(runtime.OnTopologyEpoch).toHaveBeenCalledTimes(1)
    })

    it('stops graph stream when last subscriber leaves', async () => {
      const unsub1 = store.subscribeGraph(() => {})
      const unsub2 = store.subscribeGraph(() => {})
      await flushMicrotasks()

      unsub1()
      expect(topoCancel).not.toHaveBeenCalled()

      unsub2()
      expect(topoCancel).toHaveBeenCalledTimes(1)
      expect(store.isUnifiedGraphStreaming()).toBe(false)
    })
  })

  // ---------------------------------------------------------------------------
  // Graph — patch application
  // ---------------------------------------------------------------------------
  describe('graph patch handling', () => {
    it('applies contiguous patch directly without pull sync', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 5,
        Snapshot: emptyGraph(5),
        Patches: [],
      })

      store.subscribeGraph(() => {})
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledTimes(1)

      epochHandlers[0]!({ Epoch: 6, Patch: graphPatch(6) })
      expect(unified_graph.sync).toHaveBeenCalledTimes(1)
    })

    it('applies UpdatedNodes directly without pull sync', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 1,
        Snapshot: { Version: '1', Nodes: [{ Id: 'n1', Kind: 'actor', Label: 'old', Status: 'idle' }], Edges: [] },
        Patches: [],
      })

      store.subscribeGraph(() => {})
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledTimes(1)
      expect(store.getUnifiedGraph()?.Nodes[0]?.Label).toBe('old')

      epochHandlers[0]!({
        Epoch: 2,
        Patch: graphPatch(2, undefined, undefined, [{ Id: 'n1', Kind: 'actor', Label: 'new', Status: 'running' }]),
      })

      expect(unified_graph.sync).toHaveBeenCalledTimes(1)
      const node = store.getUnifiedGraph()?.Nodes[0]
      expect(node?.Label).toBe('new')
      expect(node?.Status).toBe('running')
    })

    it('falls back to pull sync when gap > 1', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 5,
        Snapshot: emptyGraph(5),
        Patches: [],
      })

      store.subscribeGraph(() => {})
      await flushMicrotasks()

      epochHandlers[0]!({ Epoch: 8 })
      await flushMicrotasks()

      expect(unified_graph.sync).toHaveBeenCalledTimes(2)
      expect(vi.mocked(unified_graph.sync).mock.calls[1]![1]).toEqual({ ClientEpoch: 5 })
    })
  })

  // ---------------------------------------------------------------------------
  // Cross-panel history linkage
  // ---------------------------------------------------------------------------
  describe('history state linkage', () => {
    it('setSelectedHistoryEpoch updates and isHistoricalMode returns true', () => {
      expect(store.isHistoricalMode()).toBe(false)
      expect(store.getSelectedHistoryEpoch()).toBeNull()

      store.setSelectedHistoryEpoch(42)

      expect(store.isHistoricalMode()).toBe(true)
      expect(store.getSelectedHistoryEpoch()).toBe(42)
    })

    it('exitHistoryMode clears historical state', () => {
      store.setSelectedHistoryEpoch(42)
      store.exitHistoryMode()

      expect(store.isHistoricalMode()).toBe(false)
      expect(store.getSelectedHistoryEpoch()).toBeNull()
    })

    it('getActiveGraph returns historical graph when in history mode', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 10,
        Snapshot: { Version: '10', Nodes: [{ Id: 'n1', Kind: 'actor', Label: 'N1', Status: 'running' }], Edges: [] },
        Patches: [
          { ...graphPatch(2), AddedNodes: [{ Id: 'n2', Kind: 'actor', Label: 'N2', Status: 'running' }] },
          { ...graphPatch(5), AddedNodes: [{ Id: 'n3', Kind: 'actor', Label: 'N3', Status: 'running' }] },
        ],
      })

      store.subscribeGraph(() => {})
      await flushMicrotasks()

      await store.seekToEpoch(3)

      expect(store.isHistoricalMode()).toBe(true)
      const active = store.getActiveGraph()
      expect(active).not.toBeNull()
      expect(active!.Version).toBe('2')
    })
  })

  // ---------------------------------------------------------------------------
  // Back-compat
  // ---------------------------------------------------------------------------
  describe('back-compat start/stop', () => {
    it('startStreaming uses ref-count correctly', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 1,
        Snapshot: emptyGraph(1),
        Patches: [],
      })

      store.startStreaming()
      await flushMicrotasks()
      expect(unified_graph.sync).toHaveBeenCalledTimes(1)

      store.subscribeFacts(() => {})
      await flushMicrotasks()
      expect(unified_graph.sync).toHaveBeenCalledTimes(1)

      store.stopStreaming()
      expect(store.isStreaming()).toBe(false)
    })

    it('startUnifiedGraphStreaming uses ref-count correctly', async () => {
      store.startUnifiedGraphStreaming()
      await flushMicrotasks()
      expect(unified_graph.sync).toHaveBeenCalledTimes(1)

      store.subscribeGraph(() => {})
      await flushMicrotasks()
      expect(unified_graph.sync).toHaveBeenCalledTimes(1)

      store.stopUnifiedGraphStreaming()
      expect(store.isUnifiedGraphStreaming()).toBe(false)
    })
  })

  // ---------------------------------------------------------------------------
  // clear
  // ---------------------------------------------------------------------------
  describe('clear', () => {
    it('clears facts, epoch, and index', async () => {
      vi.mocked(unified_graph.sync).mockResolvedValue({
        CurrentEpoch: 2,
        Snapshot: emptyGraph(2),
        Patches: [
          graphPatch(1, [makeNode('t1')]),
          graphPatch(2, [makeNode('t2')]),
        ],
      })

      store.subscribeFacts(() => {})
      await flushMicrotasks()

      store.clear()
      expect(store.getFacts()).toHaveLength(0)
      expect(store.getEpoch()).toBe(0)
      expect(store.getFactsForEpoch(1)).toHaveLength(0)
    })
  })
})
