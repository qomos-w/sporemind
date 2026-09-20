import { useEffect, useMemo, useState } from 'react'
import type { MonoCardListItem } from '../../../domain/mono-types'
import { monoStore, graphCacheKey } from '../../panels/mono-store'

/** Graph kind backing each workflow map's dependency DAG. Written by the
 *  backend on every card-driven map change (double-write) and by external
 *  project.graph_save callers (e.g. agents on another client). */
export const WORKFLOW_TOPO_GRAPH_KIND = 'workflow_topo'

/**
 * Extract the authoritative per-task dependency list from a workflow_topo
 * graph envelope (JSON: `{nodes:[{id,...}], edges:[{from,to,kind}]}`; edge
 * semantics: From depends on To). Every graph node gets an entry — including
 * nodes with no edges, whose authoritative dependency list is empty — so a
 * graph-loaded task always overrides possibly-stale card data. Tasks absent
 * from the graph stay absent from the map and keep their card-derived deps.
 * Malformed envelopes degrade to an empty map.
 */
export function workflowTopoDeps(envelopeText: string | undefined): Map<string, string[]> {
  const deps = new Map<string, string[]>()
  if (!envelopeText) return deps
  let parsed: unknown
  try {
    parsed = JSON.parse(envelopeText)
  } catch {
    return deps
  }
  const graph = parsed as { nodes?: unknown; edges?: unknown }
  if (Array.isArray(graph.nodes)) {
    for (const node of graph.nodes) {
      const id = (node as { id?: unknown }).id
      if (typeof id === 'string' && id && !deps.has(id)) deps.set(id, [])
    }
  }
  if (Array.isArray(graph.edges)) {
    for (const edge of graph.edges) {
      const e = edge as { from?: unknown; to?: unknown; kind?: unknown }
      // Only dependency edges carry layout meaning; future data-flow kinds
      // must not produce depends_on arrows. A missing kind is treated as
      // depends_on (matches the only backend producer today).
      if (e.kind !== undefined && e.kind !== 'depends_on') continue
      if (typeof e.from !== 'string' || typeof e.to !== 'string' || !e.from || !e.to || e.from === e.to) continue
      const list = deps.get(e.from) ?? []
      if (!list.includes(e.to)) list.push(e.to)
      deps.set(e.from, list)
    }
  }
  return deps
}

/**
 * Override task cards' `data.depends_on` with the authoritative workflow_topo
 * graph dependencies. Cards without a graph entry pass through untouched.
 */
export function applyWorkflowTopoDeps(
  cards: MonoCardListItem[],
  depsByTask: ReadonlyMap<string, readonly string[]>,
): MonoCardListItem[] {
  if (depsByTask.size === 0) return cards
  return cards.map(card => {
    const deps = depsByTask.get(card.id)
    if (!deps) return card
    return { ...card, data: { ...card.data, depends_on: [...deps] } }
  })
}

/**
 * Load the workflow_topo graph of every **expanded** workflow map through
 * monoStore's graph cache and expose the merged per-task dependency map.
 * Collapsed maps are skipped (their graph is not fetched) so the panel only
 * pays the network cost for maps the user actually sees. Re-pulls whenever
 * the expanded set changes, the visible maps change, or monoStore invalidates
 * one of them (graph_changed → graphRefreshKey bump), so remote graph saves
 * made by another client automatically re-shape the topology panel.
 */
export function useWorkflowTopoDeps(
  cards: MonoCardListItem[],
  expandedMapIds: ReadonlySet<string>,
): Map<string, string[]> {
  const [storeState, setStoreState] = useState(() => monoStore.getState())
  useEffect(() => monoStore.subscribe(setStoreState), [])

  const mapIdKey = useMemo(
    () => cards
      .filter(c => c.type === 'workflow' && !c.standalone && expandedMapIds.has(c.id))
      .map(c => c.id)
      .join('\u0000'),
    [cards, expandedMapIds],
  )

  // Aggregated invalidation counter over exactly the graphs in view; a bump
  // on any of them re-runs the fetch effect below.
  const refreshTick = useMemo(() => {
    let tick = 0
    if (!mapIdKey) return tick
    for (const mapId of mapIdKey.split('\u0000')) {
      tick += storeState.graphRefreshKey[graphCacheKey(WORKFLOW_TOPO_GRAPH_KIND, mapId)] ?? 0
    }
    return tick
  }, [storeState.graphRefreshKey, mapIdKey])

  const [depsByTask, setDepsByTask] = useState<Map<string, string[]>>(() => new Map())
  useEffect(() => {
    if (!mapIdKey) {
      setDepsByTask(prev => (prev.size === 0 ? prev : new Map()))
      return
    }
    let cancelled = false
    void (async () => {
      const mapIds = mapIdKey.split('\u0000')
      const envelopes = await Promise.all(
        mapIds.map(mapId => monoStore.getGraph(WORKFLOW_TOPO_GRAPH_KIND, mapId)),
      )
      if (cancelled) return
      const merged = new Map<string, string[]>()
      for (const env of envelopes) {
        if (!env) continue
        for (const [taskId, deps] of workflowTopoDeps(env.EnvelopeText)) {
          merged.set(taskId, deps)
        }
      }
      setDepsByTask(merged)
    })()
    return () => { cancelled = true }
  }, [mapIdKey, refreshTick])

  return depsByTask
}
