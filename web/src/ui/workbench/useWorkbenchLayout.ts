import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { WorkbenchCardDescriptor } from './cardTypes'
import {
  LAYOUT_HYSTERESIS,
  LAYOUT_PROMOTE_DECAY,
  LAYOUT_SCORE_FLOOR,
  computeLayout,
  type WorkbenchLayoutResult,
  type WorkbenchPlacement,
} from './cardLayout'

/** Time the ⚑ capture proposal waits before it executes (any click vetoes). */
export const CAPTURE_PROPOSAL_MS = 3000

/**
 * Projection-driven board mutations. When supplied, board clicks dispatch to
 * the workbench actor (promote / pin / hide) instead of mutating local score
 * state — the actor stays the single source of attention truth.
 */
export interface WorkbenchMutation {
  promote: (id: string) => void
  setPinned: (id: string, pinned: boolean) => void
  setHidden: (id: string, hidden: boolean) => void
  /** ❄ Freeze / unfreeze the layout (actor-owned mode). */
  setFrozen?: (frozen: boolean) => void
  /** Enter ('' releases) a full capture (actor-owned mode). */
  setMaximized?: (id: string, reason?: string) => void
}

/** Actor-owned layout-mode state. When present it is authoritative — the
 * local fallback state is ignored and mode switches route through
 * {@link WorkbenchMutation} (the coordinator controller can flip these). */
export interface WorkbenchViewState {
  frozen: boolean
  maximizedId: string | null
}

export interface WorkbenchLayoutOptions {
  /** Ids to treat as retreated (e.g. the idle terminal card). */
  hidden?: ReadonlySet<string>
  /**
   * Projection-assigned order of the visible non-terminal cards. When set, the
   * ordering follows the actor (the local hysteresis carry-forward is bypassed).
   */
  order?: readonly string[] | null
  /**
   * Projection-driven mutations. When omitted the hook falls back to local
   * attention scoring (the projection-missing path).
   */
  mutation?: WorkbenchMutation
  /** Actor-owned layout mode (❄ freeze / full capture). */
  view?: WorkbenchViewState | null
}

export interface WorkbenchCardView {
  card: WorkbenchCardDescriptor
  placement: WorkbenchPlacement
  /** Effective attention score (descriptor + local boost, or the actor's). */
  score: number
  /** Rank-0 card renders its expanded body. */
  expanded: boolean
  focused: boolean
  pinned: boolean
  maximized: boolean
  proposing: boolean
}

export interface WorkbenchLayoutController {
  cards: WorkbenchCardView[]
  /** Hysteresis-stable order of the non-terminal visible cards. */
  order: string[]
  maximizedId: string | null
  proposal: { id: string; reason: string } | null
  frozen: boolean
  /** Click a card: promote it / capture-release / toggle pin (blackboard click). */
  select: (id: string) => void
  togglePin: (id: string) => void
  /** Raise the ⚑ capture proposal (executes after {@link CAPTURE_PROPOSAL_MS}). */
  proposeCapture: (id: string, reason: string) => void
  vetoProposal: () => void
  /** Execute a full capture immediately. */
  capture: (id: string) => void
  /** Restore the normal board from a capture. */
  release: () => void
  toggleFrozen: () => void
  setFrozen: (frozen: boolean) => void
}

/**
 * Attention-driven board state (spec §3): stable-order placement, click-to-
 * promote, pin, and the ⚑ capture proposal/veto cycle. Pure slot math lives in
 * {@link computeLayout}; this hook owns the interaction state and memoises the
 * per-card view models the wrapper component renders.
 */
export function useWorkbenchLayout(
  descriptors: readonly WorkbenchCardDescriptor[],
  options: WorkbenchLayoutOptions = {},
): WorkbenchLayoutController {
  const hidden = options.hidden
  const projectionOrder = options.order
  const mutation = options.mutation
  // Actor-owned layout mode: authoritative when present (projection mode).
  const view = options.view

  const [boosts, setBoosts] = useState<Record<string, number>>({})
  const [pinnedIds, setPinnedIds] = useState<ReadonlySet<string>>(() => new Set())
  const [localMaximizedId, setLocalMaximizedId] = useState<string | null>(null)
  const [proposal, setProposal] = useState<{ id: string; reason: string } | null>(null)
  const [localFrozen, setLocalFrozen] = useState(false)
  const proposalTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const prevOrderRef = useRef<string[] | null>(null)
  const frozenLayoutRef = useRef<WorkbenchLayoutResult | null>(null)

  const maximizedId = view ? view.maximizedId : localMaximizedId
  const frozen = view ? view.frozen : localFrozen

  const effective = useMemo(
    () =>
      descriptors.map(card => ({
        card,
        id: card.id,
        score: card.score + (boosts[card.id] ?? 0),
        pinned: card.pinned || pinnedIds.has(card.id),
        hidden: hidden?.has(card.id) ?? false,
        terminal: card.kind === 'terminal',
      })),
    [descriptors, boosts, pinnedIds, hidden],
  )

  const computed = computeLayout(effective, {
    maximizedId,
    prevOrder: projectionOrder ?? prevOrderRef.current,
  })
  // ❄ freeze (D2, blackboard layout() early-return): while frozen the board
  // keeps its last placement — score / hidden / descriptor churn does not
  // reflow it, so a frozen board is a stable snapshot.
  const layout = frozen && frozenLayoutRef.current ? frozenLayoutRef.current : computed
  if (!frozen) {
    frozenLayoutRef.current = computed
    // Carry the stable order forward for hysteresis. A capture freezes the
    // normal order, so only record it while the board is not maximized. In
    // projection mode the actor owns the order, so the local carry is skipped.
    if (!computed.maximizedId && projectionOrder == null) prevOrderRef.current = computed.order
  }

  const clearProposal = useCallback(() => {
    if (proposalTimer.current !== null) {
      clearTimeout(proposalTimer.current)
      proposalTimer.current = null
    }
  }, [])

  useEffect(() => clearProposal, [clearProposal])

  const togglePin = useCallback(
    (id: string) => {
      // Pin only takes effect on the focus card: the captured card while a
      // ⚑ capture is up, else the main slot. Pinning a side card would freeze
      // the board against promotion from an invisible position.
      const focusId = maximizedId ?? layout.order[0]
      if (id !== focusId) return
      if (mutation) {
        const target = effective.find(e => e.id === id)
        if (target) mutation.setPinned(id, !target.pinned)
        return
      }
      setPinnedIds(prev => {
        const next = new Set(prev)
        if (next.has(id)) next.delete(id)
        else next.add(id)
        return next
      })
    },
    [mutation, effective, maximizedId, layout.order],
  )

  const promote = useCallback(
    (id: string) => {
      if (mutation) {
        mutation.promote(id)
        return
      }
      const target = effective.find(e => e.id === id)
      if (!target) return
      // Mirror the actor's promote: decay visible peers first (pinned / hidden
      // / terminal exempt, floored), then boost the target above the new top —
      // repeated clicks keep reallocating attention instead of no-op-ing.
      const decays: Record<string, number> = {}
      let top = target.score
      for (const e of effective) {
        if (e.id === id) continue
        if (e.terminal || e.hidden || e.pinned) {
          top = Math.max(top, e.score)
          continue
        }
        const next = Math.max(LAYOUT_SCORE_FLOOR, e.score - LAYOUT_PROMOTE_DECAY)
        decays[e.id] = next - e.score
        top = Math.max(top, next)
      }
      const delta = top + LAYOUT_HYSTERESIS + 1 - target.score
      if (delta <= 0 && Object.keys(decays).length === 0) return
      setBoosts(prev => {
        const next = { ...prev }
        for (const [pid, d] of Object.entries(decays)) next[pid] = (next[pid] ?? 0) + d
        if (delta > 0) next[id] = (next[id] ?? 0) + delta
        return next
      })
    },
    [mutation, effective],
  )

  // applyMaximized routes a full capture through the actor when the projection
  // is live (the coordinator reads the same mode), else stays local.
  const applyMaximized = useCallback(
    (id: string | null, reason?: string) => {
      if (view) {
        mutation?.setMaximized?.(id ?? '', reason)
        return
      }
      setLocalMaximizedId(id)
    },
    [view, mutation],
  )

  const select = useCallback(
    (id: string) => {
      if (frozen || proposal) return
      const target = effective.find(e => e.id === id)
      if (!target || target.hidden) return
      if (maximizedId) {
        if (id === maximizedId) togglePin(id)
        else applyMaximized(null)
        return
      }
      // Every plain click is an attention re-assertion (promote), including
      // on the current main card — pinning is an explicit header button, so
      // repeated clicks always show feedback (peers decay) instead of the old
      // silent togglePin-on-main that made N clicks look like one promotion.
      promote(id)
    },
    [effective, frozen, proposal, maximizedId, togglePin, promote, applyMaximized],
  )

  const capture = useCallback(
    (id: string) => {
      clearProposal()
      setProposal(null)
      applyMaximized(id)
    },
    [clearProposal, applyMaximized],
  )

  const vetoProposal = useCallback(() => {
    clearProposal()
    setProposal(null)
  }, [clearProposal])

  const proposeCapture = useCallback(
    (id: string, reason: string) => {
      if (frozen || maximizedId || proposal) return
      const target = effective.find(e => e.id === id)
      if (!target || target.hidden) return
      clearProposal()
      setProposal({ id, reason })
      proposalTimer.current = setTimeout(() => {
        proposalTimer.current = null
        setProposal(null)
        applyMaximized(id, reason)
      }, CAPTURE_PROPOSAL_MS)
    },
    [clearProposal, effective, frozen, maximizedId, proposal, applyMaximized],
  )

  const release = useCallback(() => applyMaximized(null), [applyMaximized])
  const toggleFrozen = useCallback(() => {
    if (view) {
      mutation?.setFrozen?.(!view.frozen)
      return
    }
    setLocalFrozen(v => !v)
  }, [view, mutation])
  const setFrozen = useCallback(
    (next: boolean) => {
      if (view) {
        mutation?.setFrozen?.(next)
        return
      }
      setLocalFrozen(next)
    },
    [view, mutation],
  )

  const cards = useMemo(() => {
    const out: WorkbenchCardView[] = []
    for (const entry of effective) {
      const placement = layout.placements[entry.id]
      if (!placement) continue
      out.push({
        card: entry.card,
        placement,
        score: entry.score,
        focused: placement.rank === 0,
        expanded: placement.rank === 0 || entry.card.kind === 'terminal',
        pinned: entry.pinned,
        maximized: layout.maximizedId === entry.id,
        proposing: proposal?.id === entry.id,
      })
    }
    out.sort((a, b) => a.placement.rank - b.placement.rank)
    return out
  }, [effective, layout, proposal])

  return {
    cards,
    order: layout.order,
    maximizedId: layout.maximizedId,
    proposal,
    frozen,
    select,
    togglePin,
    proposeCapture,
    vetoProposal,
    capture,
    release,
    toggleFrozen,
    setFrozen,
  }
}
