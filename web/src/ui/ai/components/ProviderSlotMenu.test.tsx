import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ProviderSlotMenu, SLOT_ORDER, type ProviderSlotMenuSlots } from './ProviderSlotMenu'
import type { ModelSlot } from '../../../gen-clients/system/types'
import type { ProviderGroup, ProviderItem, ProviderOption } from './AIComposer'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  useBrowserOverlay: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: hoisted.useBrowserOverlay,
}))

function option(id: string, label: string, subtitle: string, model: string, provider: string): ProviderOption {
  return { id, label, subtitle, unit: { model, provider } }
}

const autoOption = option('auto-model', 'Auto Model', 'openai', 'auto-model', 'openai')
const routeOption = option('route-model', 'Route Model', 'anthropic', 'route-model', 'anthropic')

const autoGroup: ProviderGroup = {
  routeId: 'system',
  label: 'Auto',
  isAuto: true,
  isRoot: true,
  models: [autoOption],
  items: [{ kind: 'model', option: autoOption } as ProviderItem],
}

const customGroup: ProviderGroup = {
  routeId: 'agg-1',
  label: 'Route One',
  isAuto: false,
  isRoot: true,
  models: [routeOption],
  items: [{ kind: 'model', option: routeOption } as ProviderItem],
}

function baseSlots(overrides: Partial<ProviderSlotMenuSlots> = {}): ProviderSlotMenuSlots {
  const empty: ModelSlot = { Candidates: [] }
  return {
    Primary: empty,
    Fast: empty,
    Execution: empty,
    Review: empty,
    Summary: empty,
    ...overrides,
  }
}

describe('ProviderSlotMenu', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    hoisted.useBrowserOverlay.mockClear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  function render(props: {
    slots?: ProviderSlotMenuSlots
    groups?: ProviderGroup[]
    options?: ProviderOption[]
    open?: boolean
    onSelectSlot?: (slot: string, slotValue: ModelSlot) => void
    onClose?: () => void
  }) {
    const onSelectSlot = props.onSelectSlot ?? vi.fn()
    act(() => {
      root.render(
        <ProviderSlotMenu
          open={props.open ?? true}
          onClose={props.onClose ?? (() => {})}
          slots={props.slots ?? baseSlots()}
          groups={props.groups}
          options={props.options}
          onSelectSlot={onSelectSlot}
        />,
      )
    })
    return onSelectSlot
  }

  function rows(): HTMLElement[] {
    return Array.from(container.querySelectorAll<HTMLElement>('.ai-composer-slot-row'))
  }

  function rowFor(key: string): HTMLElement {
    const row = rows().find(el => (el.textContent ?? '').includes(key))
    if (!row) throw new Error(`slot row not found: ${key}`)
    return row
  }

  function options(): HTMLElement[] {
    return Array.from(container.querySelectorAll<HTMLElement>('.ai-composer-slot-option'))
  }

  function optionFor(label: string): HTMLElement {
    const el = options().find(o => (o.textContent ?? '').includes(label))
    if (!el) throw new Error(`option not found: ${label}`)
    return el
  }

  it('renders nothing when closed', () => {
    render({ open: false })
    expect(container.querySelector('.ai-composer-slot-menu')).toBeNull()
  })

  it('registers with the browser overlay manager while open', () => {
    render({})
    expect(hoisted.useBrowserOverlay).toHaveBeenCalledWith(true)
  })

  it('lists the five slots in the fixed documented order', () => {
    render({})
    const labels = rows().map(r => r.querySelector('.ai-composer-slot-row-name')?.textContent)
    expect(labels).toEqual([
      'dialog.newAgent.primaryModel',
      'dialog.newAgent.fastModel',
      'dialog.newAgent.executionModel',
      'dialog.newAgent.reviewModel',
      'dialog.newAgent.summaryModel',
    ])
    expect(SLOT_ORDER).toEqual(['Primary', 'Fast', 'Execution', 'Review', 'Summary'])
  })

  it('summarizes an empty slot as Auto', () => {
    render({})
    const value = rowFor('dialog.newAgent.primaryModel').querySelector('.ai-composer-slot-row-value')
    expect(value?.textContent).toContain('dialog.newAgent.auto')
  })

  it('summarizes an aggregator slot with the route label', () => {
    render({
      slots: baseSlots({ Primary: { Candidates: [{ kind: 'aggregator', AggregatorID: 'agg-1' }] } }),
      groups: [autoGroup, customGroup],
    })
    const value = rowFor('dialog.newAgent.primaryModel').querySelector('.ai-composer-slot-row-value')
    expect(value?.textContent).toContain('Route One')
  })

  it('summarizes a pinned unit slot with the model name and serving route', () => {
    const unit = { model: 'route-model', provider: 'anthropic' }
    render({
      slots: baseSlots({ Fast: { Candidates: [{ kind: 'unit', Unit: unit, AggregatorID: 'agg-1' }] } }),
      groups: [autoGroup, customGroup],
    })
    const value = rowFor('dialog.newAgent.fastModel').querySelector('.ai-composer-slot-row-value')
    expect(value?.textContent).toContain('route-model')
    expect(value?.textContent).toContain('Route One')
  })

  it('expands a slot to show route headers + concrete units before Auto', () => {
    render({ groups: [autoGroup, customGroup] })
    expect(container.querySelector('.ai-composer-slot-candidates')).toBeNull()
    act(() => { rowFor('dialog.newAgent.primaryModel').click() })
    const labels = options().map(o => o.textContent ?? '')
    expect(labels.some(l => l.includes('dialog.newAgent.auto'))).toBe(true)
    expect(labels.some(l => l.includes('Route One'))).toBe(true)
    expect(labels.some(l => l.includes('Auto Model'))).toBe(true)
    expect(labels.some(l => l.includes('Route Model'))).toBe(true)
    // Aggregator routes precede Auto, mirroring the composer dropdown.
    expect(labels.findIndex(l => l.includes('Route One')))
      .toBeLessThan(labels.findIndex(l => l.includes('dialog.newAgent.auto')))
  })

  it('selects auto for an expanded slot', () => {
    const onSelectSlot = render({ groups: [autoGroup, customGroup] })
    act(() => { rowFor('dialog.newAgent.reviewModel').click() })
    act(() => { optionFor('dialog.newAgent.auto').click() })
    expect(onSelectSlot).toHaveBeenCalledWith('Review', { Candidates: [{ kind: 'auto' }] })
  })

  it('selects an aggregator route for an expanded slot', () => {
    const onSelectSlot = render({ groups: [autoGroup, customGroup] })
    act(() => { rowFor('dialog.newAgent.primaryModel').click() })
    act(() => { optionFor('Route One').click() })
    expect(onSelectSlot).toHaveBeenCalledWith('Primary', {
      Candidates: [{ kind: 'aggregator', AggregatorID: 'agg-1' }],
    })
  })

  it('pins a custom aggregator unit through its serving route (with auto failover tail)', () => {
    const onSelectSlot = render({ groups: [autoGroup, customGroup] })
    act(() => { rowFor('dialog.newAgent.primaryModel').click() })
    act(() => { optionFor('Route Model').click() })
    expect(onSelectSlot).toHaveBeenCalledWith('Primary', {
      Candidates: [
        { kind: 'unit', Unit: routeOption.unit, AggregatorID: 'agg-1' },
        { kind: 'auto' },
      ],
    })
  })

  it('pins a system-pool unit without an aggregator id (served by the system aggregator)', () => {
    const onSelectSlot = render({ groups: [autoGroup, customGroup] })
    act(() => { rowFor('dialog.newAgent.primaryModel').click() })
    act(() => { optionFor('Auto Model').click() })
    expect(onSelectSlot).toHaveBeenCalledWith('Primary', {
      Candidates: [
        { kind: 'unit', Unit: autoOption.unit },
        { kind: 'auto' },
      ],
    })
  })

  it('falls back to a flat option list when no groups are provided', () => {
    const onSelectSlot = render({ options: [autoOption, routeOption] })
    act(() => { rowFor('dialog.newAgent.summaryModel').click() })
    const labels = options().map(o => o.textContent ?? '')
    expect(labels.some(l => l.includes('Auto Model'))).toBe(true)
    expect(labels.some(l => l.includes('Route Model'))).toBe(true)
    // Flat mode has no aggregator route headers.
    expect(labels.some(l => l.includes('Route One'))).toBe(false)
    act(() => { optionFor('Route Model').click() })
    expect(onSelectSlot).toHaveBeenCalledWith('Summary', {
      Candidates: [
        { kind: 'unit', Unit: routeOption.unit },
        { kind: 'auto' },
      ],
    })
  })

  it('marks the active candidate and the current route', () => {
    render({
      slots: baseSlots({ Primary: { Candidates: [{ kind: 'aggregator', AggregatorID: 'agg-1' }] } }),
      groups: [autoGroup, customGroup],
    })
    act(() => { rowFor('dialog.newAgent.primaryModel').click() })
    const route = optionFor('Route One')
    expect(route.className).toContain('active')
    expect(route.getAttribute('aria-checked')).toBe('true')
    expect(optionFor('dialog.newAgent.auto').getAttribute('aria-checked')).toBe('false')
  })

  it('collapses an expanded slot when its row is clicked again', () => {
    render({ groups: [autoGroup, customGroup] })
    const row = rowFor('dialog.newAgent.primaryModel')
    act(() => { row.click() })
    expect(container.querySelector('.ai-composer-slot-candidates')).not.toBeNull()
    act(() => { row.click() })
    expect(container.querySelector('.ai-composer-slot-candidates')).toBeNull()
  })

  it('closes on a left-click outside the menu (past the open guard)', () => {
    const nowSpy = vi.spyOn(performance, 'now')
    let t = 0
    nowSpy.mockImplementation(() => t)
    const onClose = vi.fn()
    render({ onClose })
    t = 300 // past the 200ms open-guard
    document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0 }))
    expect(onClose).toHaveBeenCalledTimes(1)
    nowSpy.mockRestore()
  })

  it('closes on Escape', () => {
    const onClose = vi.fn()
    render({ onClose })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not close when clicking inside the menu', () => {
    const onClose = vi.fn()
    render({ onClose, groups: [autoGroup, customGroup] })
    act(() => { rowFor('dialog.newAgent.primaryModel').click() })
    const inside = optionFor('dialog.newAgent.auto')
    const event = new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0 })
    Object.defineProperty(event, 'target', { value: inside })
    document.dispatchEvent(event)
    expect(onClose).not.toHaveBeenCalled()
  })
})
