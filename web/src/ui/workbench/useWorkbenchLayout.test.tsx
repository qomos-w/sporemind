import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import type { WorkbenchCardDescriptor } from './cardTypes'
import type { WorkbenchViewState } from './useWorkbenchLayout'
import { useWorkbenchLayout, CAPTURE_PROPOSAL_MS, type WorkbenchLayoutController, type WorkbenchMutation } from './useWorkbenchLayout'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function card(id: string, score: number, kind: WorkbenchCardDescriptor['kind'] = 'note'): WorkbenchCardDescriptor {
  return {
    id,
    kind,
    title: id,
    icon: 'file-text',
    score,
    pinned: false,
    compactMeta: { statusText: 'idle' },
    render: () => null,
  }
}

let controller: WorkbenchLayoutController | null = null

function Harness({
  descriptors,
  hidden,
  mutation,
  order,
  view,
}: {
  descriptors: WorkbenchCardDescriptor[]
  hidden?: ReadonlySet<string>
  mutation?: WorkbenchMutation
  order?: readonly string[]
  view?: WorkbenchViewState | null
}) {
  controller = useWorkbenchLayout(descriptors, { hidden, mutation, order, view })
  return (
    <div>
      {controller.cards.map(v => (
        <span key={v.card.id} data-id={v.card.id} data-zone={v.placement.zone} data-rank={v.placement.rank} />
      ))}
    </div>
  )
}

function renderHarness(
  descriptors: WorkbenchCardDescriptor[],
  hidden?: ReadonlySet<string>,
  extra?: { mutation?: WorkbenchMutation; order?: readonly string[]; view?: WorkbenchViewState | null },
) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(<Harness descriptors={descriptors} hidden={hidden} mutation={extra?.mutation} order={extra?.order} view={extra?.view} />)
  })
  return { root, container }
}

beforeEach(() => {
  vi.useFakeTimers()
  controller = null
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useWorkbenchLayout', () => {
  it('orders cards by score with the leader in the main slot', () => {
    renderHarness([card('a', 30), card('b', 20), card('c', 10)])
    expect(controller!.order).toEqual(['a', 'b', 'c'])
    expect(controller!.cards[0]!.card.id).toBe('a')
    expect(controller!.cards[0]!.placement.zone).toBe('main')
  })

  it('promotes a clicked side card past the hysteresis threshold', () => {
    renderHarness([card('a', 30), card('b', 20)])
    act(() => controller!.select('b'))
    expect(controller!.order[0]).toBe('b')
    expect(controller!.cards[0]!.card.id).toBe('b')
    expect(controller!.cards[0]!.expanded).toBe(true)
  })

  it('pins via the explicit pin toggle and keeps the pinned card in front', () => {
    renderHarness([card('a', 30), card('b', 20)])
    act(() => controller!.togglePin('a'))
    expect(controller!.cards.find(v => v.card.id === 'a')!.pinned).toBe(true)
    // A blocked promote is blocked by the pin: b can never displace a.
    act(() => controller!.select('b'))
    expect(controller!.order[0]).toBe('a')
  })

  it('re-asserts attention when clicking the main card (no silent pin toggle)', () => {
    const promote = vi.fn()
    const mutation: WorkbenchMutation = { promote, setPinned: vi.fn(), setHidden: vi.fn() }
    renderHarness([card('a', 30), card('b', 20)], undefined, { mutation })
    act(() => controller!.select('a'))
    // Plain click on the main card re-asserts through promote instead of
    // silently toggling the pin — every click keeps reallocating attention.
    expect(promote).toHaveBeenCalledWith('a')
    expect(controller!.cards.find(v => v.card.id === 'a')!.pinned).toBe(false)
    expect(controller!.order[0]).toBe('a')
  })

  it('captures a card (⚑) and releases it', () => {
    renderHarness([card('a', 30), card('b', 20)])
    act(() => controller!.capture('b'))
    expect(controller!.maximizedId).toBe('b')
    expect(controller!.cards.find(v => v.card.id === 'b')!.maximized).toBe(true)
    expect(controller!.cards.find(v => v.card.id === 'a')!.placement.zone).toBe('rail')

    // Clicking anywhere else releases the capture (blackboard click semantics).
    act(() => controller!.select('a'))
    expect(controller!.maximizedId).toBeNull()
  })

  it('executes a ⚑ capture proposal after the proposal window', () => {
    renderHarness([card('a', 30), card('b', 20)])
    act(() => controller!.proposeCapture('b', 'tests'))
    expect(controller!.proposal).toEqual({ id: 'b', reason: 'tests' })
    expect(controller!.cards.find(v => v.card.id === 'b')!.proposing).toBe(true)

    act(() => vi.advanceTimersByTime(CAPTURE_PROPOSAL_MS))
    expect(controller!.proposal).toBeNull()
    expect(controller!.maximizedId).toBe('b')
  })

  it('vetoes a capture proposal before it fires', () => {
    renderHarness([card('a', 30), card('b', 20)])
    act(() => controller!.proposeCapture('b', 'tests'))
    act(() => controller!.vetoProposal())
    expect(controller!.proposal).toBeNull()
    act(() => vi.advanceTimersByTime(CAPTURE_PROPOSAL_MS))
    expect(controller!.maximizedId).toBeNull()
  })

  it('freezes the board: clicks no longer promote or reorder', () => {
    renderHarness([card('a', 30), card('b', 20)])
    act(() => controller!.toggleFrozen())
    expect(controller!.frozen).toBe(true)
    act(() => controller!.select('b'))
    expect(controller!.order[0]).toBe('a')
  })

  it('excludes a hidden terminal from placement', () => {
    renderHarness([card('a', 30), card('term', 34, 'terminal')], new Set(['term']))
    expect(controller!.cards.map(v => v.card.id)).toEqual(['a'])
    expect(controller!.cards[0]!.placement.zone).toBe('main')
  })

  it('keeps the frozen layout stable while scores change (D2)', () => {
    const { root } = renderHarness([card('a', 30), card('b', 20)])
    expect(controller!.order).toEqual(['a', 'b'])
    act(() => controller!.toggleFrozen())

    // A score flip that would otherwise swap the order is ignored while frozen.
    act(() => {
      root.render(<Harness descriptors={[card('a', 10), card('b', 40)]} />)
    })
    expect(controller!.frozen).toBe(true)
    expect(controller!.order).toEqual(['a', 'b'])
    expect(controller!.cards[0]!.card.id).toBe('a')

    // Unfreezing adopts the projection order.
    act(() => controller!.toggleFrozen())
    expect(controller!.order[0]).toBe('b')
  })

  it('routes click promotion and pinning to the injected mutation callbacks', () => {
    const promote = vi.fn()
    const setPinned = vi.fn()
    const setHidden = vi.fn()
    const mutation: WorkbenchMutation = { promote, setPinned, setHidden }
    renderHarness([card('a', 30), card('b', 20)], undefined, { mutation })

    act(() => controller!.select('b'))
    expect(promote).toHaveBeenCalledWith('b')
    expect(setPinned).not.toHaveBeenCalled()

    // Clicking the current leader re-asserts through promote (no silent pin).
    act(() => controller!.select('a'))
    expect(promote).toHaveBeenCalledWith('a')
    expect(setPinned).not.toHaveBeenCalled()

    // Pinning is the explicit toggle, routed to the backend.
    act(() => controller!.togglePin('a'))
    expect(setPinned).toHaveBeenCalledWith('a', true)
  })

  it('follows the projection-supplied order', () => {
    renderHarness([card('a', 30), card('b', 20)], undefined, { order: ['b', 'a'] })
    expect(controller!.order).toEqual(['b', 'a'])
    expect(controller!.cards[0]!.card.id).toBe('b')
  })

  it('routes layout-mode switches through the actor when view state is live', () => {
    const setFrozen = vi.fn()
    const setMaximized = vi.fn()
    const mutation: WorkbenchMutation = { promote: vi.fn(), setPinned: vi.fn(), setHidden: vi.fn(), setFrozen, setMaximized }
    const { root } = renderHarness([card('a', 30), card('b', 20)], undefined, {
      mutation,
      view: { frozen: false, maximizedId: null },
    })

    // ❄ and full capture leave the local fallback: the actor owns the mode.
    act(() => controller!.toggleFrozen())
    expect(setFrozen).toHaveBeenCalledWith(true)

    act(() => controller!.capture('b'))
    expect(setMaximized).toHaveBeenCalledWith('b', undefined)

    act(() => controller!.proposeCapture('a', 'deploy 完成'))
    act(() => vi.advanceTimersByTime(CAPTURE_PROPOSAL_MS))
    expect(setMaximized).toHaveBeenCalledWith('a', 'deploy 完成')

    act(() => controller!.release())
    expect(setMaximized).toHaveBeenLastCalledWith('', undefined)
    root.unmount()
  })

  it('follows actor-owned view state: remote freeze holds placement, remote capture takes the field', () => {
    // Remote capture (the coordinator maximized a card): the view mirrors it
    // without any local interaction.
    const { root } = renderHarness([card('a', 30), card('b', 20)], undefined, {
      view: { frozen: false, maximizedId: 'b' },
    })
    expect(controller!.maximizedId).toBe('b')
    root.unmount()

    // Remote freeze: clicks stop reordering (actor-owned frozen flag).
    const frozen = renderHarness([card('a', 30), card('b', 20)], undefined, {
      view: { frozen: true, maximizedId: null },
    })
    act(() => controller!.select('b'))
    expect(controller!.order[0]).toBe('a')
    frozen.root.unmount()
  })
})
