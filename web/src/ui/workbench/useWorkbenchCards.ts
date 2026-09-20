import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { client } from '../../application/generated-client'
import { getSnapshot, watchSnapshot } from '../../gen-clients/workbench/projection-client'
import {
  promote as promoteCard,
  setFrozen as setFrozenCall,
  setHidden as setHiddenCard,
  setMaximized as setMaximizedCall,
  setPinned as setPinnedCard,
  upsertCard as upsertCardCall,
} from '../../gen-clients/workbench/client'
import type {
  WorkbenchCardState,
  WorkbenchSnapshot,
  WorkbenchUpsertCardReq,
} from '../../gen-clients/system/types'
import { selectWorkbenchLayout, type WorkbenchLayout } from './workbenchLayout'
import { pascalize } from './wireShape'

export type { WorkbenchLayout } from './workbenchLayout'
export { selectWorkbenchLayout } from './workbenchLayout'

export interface UseWorkbenchCardsResult {
  /** Cards after the includeHidden filter. */
  cards: WorkbenchCardState[]
  /** Slot grouping over the full snapshot (including hidden cards). */
  layout: WorkbenchLayout
  generation: number
  /** ❄ layout freeze (actor-owned mode, persisted). */
  frozen: boolean
  /** Full-capture card id ('' = none; actor-owned mode, ephemeral). */
  maximized: string
  loading: boolean
  /**
   * True once the projection has answered at least once. The board uses this to
   * decide whether to run projection-driven (true) or fall back to local layout.
   */
  ready: boolean
  error: string | null
  refresh: () => Promise<void>
  promote: (id: string) => Promise<void>
  setPinned: (id: string, pinned: boolean) => Promise<void>
  setHidden: (id: string, hidden: boolean) => Promise<void>
  /** Declare / update a card's static metadata (app / agent回流). */
  upsertCard: (req: WorkbenchUpsertCardReq) => Promise<void>
  /** ❄ Freeze / unfreeze the layout (actor-owned mode, persisted). */
  setFrozen: (frozen: boolean) => Promise<void>
  /** Enter ('' releases) a full capture (actor-owned mode, ephemeral). */
  setMaximized: (id: string, reason?: string) => Promise<void>
}

/**
 * useWorkbenchCards subscribes to the workbench attention/layout projection.
 *
 * It fetches the initial snapshot via `workbench.snapshot` (and the
 * `gospore.projection.get` component read behind getSnapshot), then follows the
 * live `gospore.projection.watch` stream, so layout changes driven by app
 * lifecycle / agent activity / user promotion arrive without polling. Mutating
 * helpers call the lane-routed write callables and apply the returned snapshot
 * immediately (the projection push also follows on the next invoke).
 */
/**
 * Normalize an incoming projection snapshot: handle the lowerFirst projection
 * wire keys (see pascalize) and the empty-board case, where `Cards` is omitted
 * (Go omitempty on the empty slice) and would otherwise surface as `undefined`
 * and crash every iteration downstream ("t is not iterable").
 */
export function normalizeSnapshot(snap?: WorkbenchSnapshot | null): WorkbenchSnapshot {
  const s = pascalize(snap ?? {}) as WorkbenchSnapshot
  // Boundary guard: a card state without a usable Id would crash every
  // id-driven consumer (chatActorId startsWith, descriptor merge) and unmount
  // the whole surface — drop it instead.
  const cards = (s.Cards ?? []).filter(
    (card): card is WorkbenchCardState => !!card && typeof card.Id === 'string' && card.Id !== '',
  )
  return {
    Cards: cards,
    Generation: s.Generation ?? 0,
    Frozen: s.Frozen ?? false,
    Maximized: s.Maximized ?? '',
  }
}

export function useWorkbenchCards(includeHidden = false): UseWorkbenchCardsResult {
  const [snapshot, setSnapshot] = useState<WorkbenchSnapshot>(normalizeSnapshot())
  const [loading, setLoading] = useState(true)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const activeRef = useRef(true)

  const refresh = useCallback(async () => {
    try {
      const snap = await getSnapshot(client)
      if (activeRef.current) {
        setSnapshot(normalizeSnapshot(snap))
        setReady(true)
        setError(null)
      }
    } catch (err) {
      if (activeRef.current) setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (activeRef.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    activeRef.current = true
    let cancelled = false
    void (async () => {
      // Materialise the projection before subscribing: the initial fetch both
      // seeds the UI and guarantees the component snapshot exists, so the
      // watch stream has a baseline even before its first push.
      await refresh()
      if (cancelled) return
      try {
        for await (const next of watchSnapshot(client)) {
          if (cancelled) return
          setSnapshot(normalizeSnapshot(next))
          setReady(true)
          setLoading(false)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      }
    })()
    return () => {
      cancelled = true
      activeRef.current = false
    }
  }, [refresh])

  const applySnapshot = useCallback((snap: WorkbenchSnapshot) => {
    if (activeRef.current) setSnapshot(normalizeSnapshot(snap))
  }, [])

  const promote = useCallback(async (id: string) => {
    try {
      applySnapshot(await promoteCard(client, { Id: id }))
    } catch (err) {
      if (activeRef.current) setError(err instanceof Error ? err.message : String(err))
    }
  }, [applySnapshot])

  const setPinned = useCallback(async (id: string, pinned: boolean) => {
    try {
      applySnapshot(await setPinnedCard(client, { Id: id, Pinned: pinned }))
    } catch (err) {
      if (activeRef.current) setError(err instanceof Error ? err.message : String(err))
    }
  }, [applySnapshot])

  const setHidden = useCallback(async (id: string, hidden: boolean) => {
    try {
      applySnapshot(await setHiddenCard(client, { Id: id, Hidden: hidden }))
    } catch (err) {
      if (activeRef.current) setError(err instanceof Error ? err.message : String(err))
    }
  }, [applySnapshot])

  const upsertCard = useCallback(async (req: WorkbenchUpsertCardReq) => {
    try {
      applySnapshot(await upsertCardCall(client, req))
    } catch (err) {
      if (activeRef.current) setError(err instanceof Error ? err.message : String(err))
    }
  }, [applySnapshot])

  const setFrozen = useCallback(async (frozen: boolean) => {
    try {
      applySnapshot(await setFrozenCall(client, { Frozen: frozen }))
    } catch (err) {
      if (activeRef.current) setError(err instanceof Error ? err.message : String(err))
    }
  }, [applySnapshot])

  const setMaximized = useCallback(async (id: string, reason?: string) => {
    try {
      applySnapshot(await setMaximizedCall(client, { Id: id, ...(reason ? { Reason: reason } : {}) }))
    } catch (err) {
      if (activeRef.current) setError(err instanceof Error ? err.message : String(err))
    }
  }, [applySnapshot])

  const cards = useMemo(
    () => (includeHidden ? snapshot.Cards : snapshot.Cards.filter(card => card.Slot !== 'hidden')),
    [snapshot, includeHidden],
  )
  const layout = useMemo(() => selectWorkbenchLayout(snapshot.Cards), [snapshot])

  return {
    cards,
    layout,
    generation: snapshot.Generation,
    frozen: snapshot.Frozen ?? false,
    maximized: snapshot.Maximized ?? '',
    loading,
    ready,
    error,
    refresh,
    promote,
    setPinned,
    setHidden,
    upsertCard,
    setFrozen,
    setMaximized,
  }
}
