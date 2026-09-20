import type { UnifiedGraph, TopologyHistoryEntry, TopologyEpochEvent, UnifiedGraphNode } from '../../gen-types/observation'
import { client } from '../../application/generated-client'
import { WebSocketTransport } from '@qomos/gospore-client'
import { OnTopologyEpoch } from '../../gen-clients/runtime/client'
import * as unified_graph from '../../gen-clients/unified_graph/client'
import { applyPatches } from './graphPatch'

export interface FactSnapshot {
  Kind: string
  Epoch: number
  TaskId: string
  ObservedAt: string
}

const MAX_FACTS = 500

class ObservationStore {
  private facts: FactSnapshot[] = []
  private currentEpoch = 0
  private version = 0
  private listeners = new Set<() => void>()

  // -- Ref-counted streaming --
  private graphRefCount = 0
  private factsRefCount = 0
  private graphStreaming = false
  private factsStreaming = false
  private graphCleanup?: () => void
  private connectedCleanup?: () => void

  // -- UnifiedGraph --
  private unifiedGraph: UnifiedGraph | null = null
  private topoEpoch = 0
  private topoHistoryEntries: TopologyHistoryEntry[] = []

  // -- Shared history state (cross-panel linkage) --
  private selectedHistoryEpoch: number | null = null
  private historicalGraph: UnifiedGraph | null = null

  // -- Epoch-to-facts index (unified data access) --
  private factsByEpoch = new Map<number, FactSnapshot[]>()

  // -- Retry backoff for initial graph sync --
  private graphStreamRetryCount = 0
  private graphStreamRetryTimer?: ReturnType<typeof setTimeout>

  // ---------------------------------------------------------------------------
  // Base subscription (used by both useSyncExternalStore paths)
  // ---------------------------------------------------------------------------
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  getVersion = () => this.version

  private emit() {
    this.version++
    for (const fn of this.listeners) fn()
  }

  // ---------------------------------------------------------------------------
  // Graph stream (ref-counted)
  // ---------------------------------------------------------------------------
  subscribeGraph = (listener: () => void): (() => void) => {
    if (this.graphRefCount++ === 0) {
      this.doStartGraphStream()
    }
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
      if (--this.graphRefCount === 0) {
        this.doStopGraphStream()
      }
    }
  }

  private doStartGraphStream() {
    if (this.graphStreaming) return
    this.graphStreaming = true

    const pendingEvents: TopologyEpochEvent[] = []
    let pullDone = false

    // On reconnect, the transport replays subscriptions from sinceSeqNo but
    // we can't trust that to fully bridge a long disconnect — pull once to
    // re-anchor against the server's current epoch.
    const transport = client.getTransport()
    if (transport instanceof WebSocketTransport) {
      this.connectedCleanup = transport.onConnected(({ isReconnect }) => {
        if (isReconnect && this.graphStreaming) {
          this.pullSync()
        }
      })
    }

    this.graphCleanup = OnTopologyEpoch(client, (event) => {
      console.debug('[observationStore] OnTopologyEpoch event:', { epoch: event.Epoch, hasPatch: !!event.Patch, pullDone })
      if (!pullDone) {
        pendingEvents.push(event)
        return
      }
      this.handleEpochEvent(event)
    })

    unified_graph.sync(client, { ClientEpoch: 0 })
      .then((resp) => {
        const patches = resp.Patches ?? []
        console.debug('[observationStore] sync resp:', {
          currentEpoch: resp.CurrentEpoch,
          snapshotNodes: resp.Snapshot?.Nodes.length ?? 0,
          snapshotEdges: resp.Snapshot?.Edges.length ?? 0,
          patchCount: patches.length,
          snapshotNodeIds: resp.Snapshot?.Nodes.map(n => n.Id) ?? [],
        })
        if (resp.Snapshot) {
          this.unifiedGraph = resp.Snapshot
        }
        // For initial sync (ClientEpoch: 0) the snapshot is already the
        // current enriched graph. Historical patches must not be applied on
        // top — they would overwrite enrichment (labels, status, context
        // budget) with raw topology data.
        this.topoEpoch = resp.CurrentEpoch
        this.facts = this.deriveFactsFromPatches(patches)
        this.rebuildFactsByEpoch()
        if (this.facts.length > 0) {
          this.currentEpoch = this.facts[this.facts.length - 1]!.Epoch
        }
        pullDone = true
        this.graphStreamRetryCount = 0
        this.emit()
        for (const ev of pendingEvents) {
          this.handleEpochEvent(ev)
        }
        pendingEvents.length = 0
      })
      .catch((err) => {
        console.error('[observationStore] sync initial pull failed:', err)
        pullDone = true
        // Retry with exponential backoff if still streaming.
        if (this.graphStreaming && this.graphStreamRetryCount < 6) {
          const delay = Math.min(2000 * Math.pow(2, this.graphStreamRetryCount), 30000)
          this.graphStreamRetryCount++
          this.graphStreamRetryTimer = setTimeout(() => {
            if (this.graphStreaming) {
              this.doStopGraphStream() // clean up old subscriptions/timers
              this.doStartGraphStream()
            }
          }, delay)
        }
      })

    this.graphStreamRetryCount = 0
    this.refreshHistory()
    this.emit()
  }

  private doStopGraphStream() {
    this.graphCleanup?.()
    this.connectedCleanup?.()
    this.graphCleanup = undefined
    this.connectedCleanup = undefined
    if (this.graphStreamRetryTimer) {
      clearTimeout(this.graphStreamRetryTimer)
      this.graphStreamRetryTimer = undefined
    }
    this.graphStreaming = false
    this.graphStreamRetryCount = 0
    this.selectedHistoryEpoch = null
    this.historicalGraph = null
    this.emit()
  }

  // ---------------------------------------------------------------------------
  // Facts stream (ref-counted)
  // ---------------------------------------------------------------------------
  subscribeFacts = (listener: () => void): (() => void) => {
    if (this.factsRefCount++ === 0) {
      this.doStartFactsStream()
    }
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
      if (--this.factsRefCount === 0) {
        this.doStopFactsStream()
      }
    }
  }

  private doStartFactsStream() {
    if (this.factsStreaming) return
    this.factsStreaming = true

    // Derive initial facts from topology patches via sync callable.
    unified_graph.sync(client, { ClientEpoch: 0 })
      .then((resp) => {
        this.facts = this.deriveFactsFromPatches(resp.Patches ?? [])
        this.rebuildFactsByEpoch()
        if (this.facts.length > 0) {
          this.currentEpoch = this.facts[this.facts.length - 1]!.Epoch
        }
        this.emit()
      })
      .catch((err) => {
        console.error('[observationStore] fact sync failed:', err)
      })

    // Real-time facts are derived from topology-epoch patches in handleEpochEvent.

    this.emit()
  }

  private doStopFactsStream() {
    this.factsStreaming = false
    this.emit()
  }

  private rebuildFactsByEpoch() {
    this.factsByEpoch.clear()
    for (const f of this.facts) {
      const list = this.factsByEpoch.get(f.Epoch) ?? []
      list.push(f)
      this.factsByEpoch.set(f.Epoch, list)
    }
  }

  private deriveFactsFromPatches(patches: Array<{ Epoch?: number; Timestamp?: string; AddedNodes?: UnifiedGraphNode[]; RemovedNodes?: string[] }>): FactSnapshot[] {
    const result: FactSnapshot[] = []
    for (const patch of patches) {
      const epoch = patch.Epoch ?? 0
      const ts = patch.Timestamp ?? new Date().toISOString()
      for (const n of (patch.AddedNodes ?? [])) {
        result.push({ Kind: 'actor.created', Epoch: epoch, TaskId: n.Id, ObservedAt: ts })
      }
      for (const id of (patch.RemovedNodes ?? [])) {
        result.push({ Kind: 'actor.deleted', Epoch: epoch, TaskId: id, ObservedAt: ts })
      }
    }
    // Cap at MAX_FACTS, keep most recent
    if (result.length > MAX_FACTS) {
      return result.slice(-MAX_FACTS)
    }
    return result
  }

  // ---------------------------------------------------------------------------
  // Fact accessors
  // ---------------------------------------------------------------------------
  getFacts(): FactSnapshot[] {
    return this.facts
  }

  getEpoch(): number {
    return this.currentEpoch
  }

  isStreaming(): boolean {
    return this.factsStreaming
  }

  clear() {
    this.facts = []
    this.currentEpoch = 0
    this.factsByEpoch.clear()
    this.emit()
  }

  latestFacts(n: number): FactSnapshot[] {
    return this.facts.slice(-n)
  }

  factsByKind(kind: string): FactSnapshot[] {
    return this.facts.filter(f => f.Kind === kind)
  }

  // -- Epoch-to-facts index (unified data access) --

  getFactsForEpoch(epoch: number): FactSnapshot[] {
    return this.factsByEpoch.get(epoch) ?? []
  }

  getFactsUpToEpoch(epoch: number): FactSnapshot[] {
    return this.facts.filter(f => f.Epoch <= epoch)
  }

  // ---------------------------------------------------------------------------
  // UnifiedGraph accessors
  // ---------------------------------------------------------------------------
  getUnifiedGraph(): UnifiedGraph | null {
    return this.unifiedGraph
  }

  getActiveGraph(): UnifiedGraph | null {
    return this.historicalGraph ?? this.unifiedGraph
  }

  isUnifiedGraphStreaming(): boolean {
    return this.graphStreaming
  }

  getTopoEpoch(): number {
    return this.topoEpoch
  }

  getHistoryEntries(): TopologyHistoryEntry[] {
    return this.topoHistoryEntries
  }

  // ---------------------------------------------------------------------------
  // Shared history state (cross-panel linkage)
  // ---------------------------------------------------------------------------
  getSelectedHistoryEpoch(): number | null {
    return this.selectedHistoryEpoch
  }

  isHistoricalMode(): boolean {
    return this.selectedHistoryEpoch !== null
  }

  setSelectedHistoryEpoch(epoch: number | null) {
    if (this.selectedHistoryEpoch === epoch) return
    this.selectedHistoryEpoch = epoch
    this.emit()
  }

  private handleEpochEvent(event: TopologyEpochEvent) {
    console.debug('[observationStore] handleEpochEvent:', {
      eventEpoch: event.Epoch,
      topoEpoch: this.topoEpoch,
      hasPatch: !!event.Patch,
      hasGraph: !!this.unifiedGraph,
      addedNodes: event.Patch?.AddedNodes?.length ?? 0,
      removedNodes: event.Patch?.RemovedNodes?.length ?? 0,
      updatedNodes: event.Patch?.UpdatedNodes?.length ?? 0,
    })
    if (event.Epoch <= this.topoEpoch) {
      console.debug('[observationStore] event epoch <= topoEpoch, ignoring')
      return
    }

    // Derive facts from patch.
    if (event.Patch) {
      const newFacts: FactSnapshot[] = []
      for (const n of (event.Patch.AddedNodes ?? [])) {
        newFacts.push({
          Kind: 'actor.created',
          Epoch: event.Epoch,
          TaskId: n.Id,
          ObservedAt: event.Patch.Timestamp ?? new Date().toISOString(),
        })
      }
      for (const id of (event.Patch.RemovedNodes ?? [])) {
        newFacts.push({
          Kind: 'actor.deleted',
          Epoch: event.Epoch,
          TaskId: id,
          ObservedAt: event.Patch.Timestamp ?? new Date().toISOString(),
        })
      }
      if (newFacts.length > 0) {
        this.facts = [...this.facts.slice(-(MAX_FACTS - newFacts.length)), ...newFacts]
        this.currentEpoch = event.Epoch
        for (const f of newFacts) {
          const list = this.factsByEpoch.get(f.Epoch) ?? []
          list.push(f)
          this.factsByEpoch.set(f.Epoch, list)
        }
      }
    }

    // Apply patch to graph.
    if (event.Patch && event.Epoch === this.topoEpoch + 1 && this.unifiedGraph) {
      console.debug('[observationStore] applying patch directly')
      this.unifiedGraph = applyPatches(this.unifiedGraph, [event.Patch])
      this.topoEpoch = event.Epoch
      this.emit()
      return
    }
    console.debug('[observationStore] falling back to pullSync')
    this.pullSync()
  }

  pullSync() {
    unified_graph.sync(client, { ClientEpoch: this.topoEpoch })
      .then((resp) => {
        if (resp.Snapshot) {
          this.unifiedGraph = resp.Snapshot
        }
        if (resp.Patches && resp.Patches.length > 0 && this.unifiedGraph) {
          this.unifiedGraph = applyPatches(this.unifiedGraph, resp.Patches)
        }
        this.topoEpoch = resp.CurrentEpoch
        this.emit()
      })
      .catch((err) => {
        console.error('[observationStore] unified_graph.sync pull failed:', err)
        // Retry once after 2s
        setTimeout(() => this.pullSync(), 2000)
      })
  }

  private refreshHistory() {
    unified_graph.history(client)
      .then((resp) => {
        this.topoHistoryEntries = resp.Entries ?? []
        this.emit()
      })
      .catch((err) => {
        console.error('[observationStore] unified_graph.history failed:', err)
      })
  }

  async seekToEpoch(epoch: number): Promise<void> {
    if (epoch === this.topoEpoch || epoch === this.topoEpoch - 1) {
      this.selectedHistoryEpoch = null
      this.historicalGraph = null
      this.emit()
      return
    }

    const resp = await unified_graph.sync(client, { ClientEpoch: 0 })
    if (resp.Snapshot) {
      let graph = resp.Snapshot
      if (resp.Patches) {
        for (const p of resp.Patches) {
          if (p.Epoch <= epoch) {
            graph = applyPatches(graph, [p])
          }
        }
      }
      this.historicalGraph = graph
      this.selectedHistoryEpoch = epoch
      this.emit()
    }
  }

  exitHistoryMode() {
    this.selectedHistoryEpoch = null
    this.historicalGraph = null
    this.emit()
  }

  // ---------------------------------------------------------------------------
  // Back-compat: direct start/stop (used by consumers not yet migrated)
  // ---------------------------------------------------------------------------
  startStreaming() {
    if (this.factsRefCount === 0) {
      this.doStartFactsStream()
      this.factsRefCount = 1
    }
  }

  stopStreaming() {
    if (this.factsRefCount > 0) {
      this.doStopFactsStream()
      this.factsRefCount = 0
    }
  }

  startUnifiedGraphStreaming() {
    if (this.graphRefCount === 0) {
      this.doStartGraphStream()
      this.graphRefCount = 1
    }
  }

  stopUnifiedGraphStreaming() {
    if (this.graphRefCount > 0) {
      this.doStopGraphStream()
      this.graphRefCount = 0
    }
  }
}

export { ObservationStore }
export const observationStore = new ObservationStore()
