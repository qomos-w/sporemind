import { describe, it, expect, vi, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import type { MonoCardListItem } from '../../../domain/mono-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  // Return the key verbatim so assertions stay on keys.
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('../browserOverlay', () => ({
  useBrowserOverlay: () => {},
}))

// Import AFTER mocks are set up.
import { TopologyCardContextMenu, type CardMenuTarget } from './TopologyCardContextMenu'

function makeCard(id: string, over: Partial<MonoCardListItem> = {}): MonoCardListItem {
  return {
    id,
    type: 'workflow',
    tags: [],
    list: [],
    created: '2026-01-01T00:00:00Z',
    modified: '2026-01-01T00:00:00Z',
    ...over,
  }
}

function baseProps(over: Partial<Parameters<typeof TopologyCardContextMenu>[0]> = {}) {
  const onClose = vi.fn()
  const onOpenCard = vi.fn()
  const onRename = vi.fn()
  const onAddTag = vi.fn()
  const onAddChild = vi.fn()
  const onSetVisual = vi.fn()
  const onDuplicate = vi.fn()
  const onCopyPath = vi.fn()
  const onDelete = vi.fn()
  const onFilterSubtree = vi.fn()
  const onFilterLineage = vi.fn()
  const onChat = vi.fn()
  const onAssignGoal = vi.fn()
  return {
    target: null as CardMenuTarget | null,
    context: 'topology' as const,
    onClose,
    onOpenCard,
    onRename,
    onAddTag,
    onAddChild,
    onSetVisual,
    onDuplicate,
    onCopyPath,
    onDelete,
    onFilterSubtree,
    onFilterLineage,
    onChat,
    onAssignGoal,
    ...over,
  }
}

function renderMenu(props: Parameters<typeof TopologyCardContextMenu>[0]) {
  let root: Root | null = null
  let container: HTMLDivElement
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  act(() => {
    root!.render(<TopologyCardContextMenu {...props} />)
  })
  const update = (next: Parameters<typeof TopologyCardContextMenu>[0]) => {
    act(() => {
      root!.render(<TopologyCardContextMenu {...next} />)
    })
  }
  return {
    labels: () => Array.from(container.querySelectorAll('.topology-card-context-menu-label')).map(el => el.textContent ?? ''),
    instances: () => Array.from(container.querySelectorAll('.topology-card-context-menu-instance .topology-card-context-menu-label')).map(el => el.textContent ?? ''),
    clickItem: (label: string) => {
      const buttons = Array.from(container.querySelectorAll('button.topology-card-context-menu-item'))
      const btn = buttons.find(b => (b.querySelector('.topology-card-context-menu-label')?.textContent ?? '') === label)
      if (!btn) throw new Error(`menu item not found: ${label}`)
      act(() => { fireEvent.click(btn) })
    },
    update,
    cleanup: () => {
      act(() => { root!.unmount() })
      container.remove()
    },
  }
}

describe('TopologyCardContextMenu template/instance/scheduler items', () => {
  afterEach(() => { document.body.innerHTML = '' })

  it('shows Instantiate + View Instances only for template cards (data.template === true)', () => {
    const tpl = makeCard('tpl::MyFlow', { data: { template: true } })
    const props = baseProps({
      target: { card: tpl, x: 10, y: 10 },
      onInstantiateFromTemplate: vi.fn(),
      onViewInstances: vi.fn(),
    })
    const menu = renderMenu(props)
    const labels = menu.labels()
    expect(labels).toContain('topologyCardContextMenu.instantiateFromTemplate')
    expect(labels).toContain('topologyCardContextMenu.viewInstances')
    menu.cleanup()
  })

  it('hides Instantiate/View Instances for non-template workflow cards', () => {
    const plain = makeCard('MapA', { data: {} })
    const props = baseProps({
      target: { card: plain, x: 10, y: 10 },
      onInstantiateFromTemplate: vi.fn(),
      onViewInstances: vi.fn(),
    })
    const menu = renderMenu(props)
    const labels = menu.labels()
    expect(labels).not.toContain('topologyCardContextMenu.instantiateFromTemplate')
    expect(labels).not.toContain('topologyCardContextMenu.viewInstances')
    menu.cleanup()
  })

  it('clicking Instantiate fires onInstantiateFromTemplate and closes the menu', () => {
    const onInstantiateFromTemplate = vi.fn()
    const onClose = vi.fn()
    const tpl = makeCard('tpl::MyFlow', { data: { template: true } })
    const props = baseProps({
      target: { card: tpl, x: 10, y: 10 },
      onInstantiateFromTemplate,
      onClose,
    })
    const menu = renderMenu(props)
    menu.clickItem('topologyCardContextMenu.instantiateFromTemplate')
    expect(onInstantiateFromTemplate).toHaveBeenCalledWith(tpl)
    expect(onClose).toHaveBeenCalled()
    menu.cleanup()
  })

  it('View Instances keeps the menu open and renders the fetched runs as sub-items', () => {
    const onViewInstances = vi.fn()
    const onOpenInstanceMap = vi.fn()
    const onClose = vi.fn()
    const tpl = makeCard('tpl::MyFlow', { data: { template: true } })
    // Start with the runs not yet loaded: clicking must not close the menu.
    const props = baseProps({
      target: { card: tpl, x: 10, y: 10 },
      onViewInstances,
      onOpenInstanceMap,
      onClose,
    })
    const menu = renderMenu(props)
    menu.clickItem('topologyCardContextMenu.viewInstances')
    expect(onViewInstances).toHaveBeenCalledWith(tpl)
    expect(onClose).not.toHaveBeenCalled()
    // Runs arrive: the same menu instance re-renders with instanceRuns and
    // the sub-items appear (viewingRuns is still true).
    menu.update({ ...props, instanceRuns: ['inst::1::tpl::MyFlow', 'inst::2::tpl::MyFlow'] })
    expect(menu.instances()).toEqual(['inst::1::tpl::MyFlow', 'inst::2::tpl::MyFlow'])
    // Clicking a sub-item locates that instance map and closes the menu.
    const btn = Array.from(document.querySelectorAll('button.topology-card-context-menu-instance'))[0] as HTMLButtonElement
    act(() => { fireEvent.click(btn) })
    expect(onOpenInstanceMap).toHaveBeenCalledWith('inst::1::tpl::MyFlow')
    expect(onClose).toHaveBeenCalledTimes(1)
    menu.cleanup()
  })

  it('shows New Scheduled Task only for template cards and fires onCreateScheduledTask on click', () => {
    const onCreateScheduledTask = vi.fn()
    const onClose = vi.fn()
    const tpl = makeCard('tpl::MyFlow', { data: { template: true } })
    const props = baseProps({ target: { card: tpl, x: 10, y: 10 }, onCreateScheduledTask, onClose })
    const menu = renderMenu(props)
    expect(menu.labels()).toContain('topologyCardContextMenu.newScheduledTask')
    menu.clickItem('topologyCardContextMenu.newScheduledTask')
    expect(onCreateScheduledTask).toHaveBeenCalledWith(tpl)
    expect(onClose).toHaveBeenCalled()
    menu.cleanup()

    // Non-template workflow cards must not see the entry.
    const plain = makeCard('MapA', { data: {} })
    const props2 = baseProps({ target: { card: plain, x: 10, y: 10 }, onCreateScheduledTask })
    const menu2 = renderMenu(props2)
    expect(menu2.labels()).not.toContain('topologyCardContextMenu.newScheduledTask')
    menu2.cleanup()
  })

  it('shows Locate Scheduler only when instance_of AND scheduler_card_id are present', () => {
    const instance = makeCard('inst::x::tpl::MyFlow', { data: { instance_of: 'tpl::MyFlow', scheduler_card_id: 'scheduler:daily' } })
    const props = baseProps({
      target: { card: instance, x: 10, y: 10 },
      onLocateScheduler: vi.fn(),
    })
    const menu = renderMenu(props)
    expect(menu.labels()).toContain('topologyCardContextMenu.locateScheduler')
    menu.cleanup()

    // Without scheduler_card_id the item hides itself (field lands from the
    // backend line later — the entry must degrade gracefully).
    const noScheduler = makeCard('inst::x::tpl::MyFlow', { data: { instance_of: 'tpl::MyFlow' } })
    const props2 = baseProps({
      target: { card: noScheduler, x: 10, y: 10 },
      onLocateScheduler: vi.fn(),
    })
    const menu2 = renderMenu(props2)
    expect(menu2.labels()).not.toContain('topologyCardContextMenu.locateScheduler')
    menu2.cleanup()
  })

  it('shows Run Now only for scheduler cards (tags contain scheduler) and fires onRunNow', () => {
    const scheduler = makeCard('scheduler:daily', { type: 'scheduler', tags: ['scheduler'], data: {} })
    const onRunNow = vi.fn()
    const onClose = vi.fn()
    const props = baseProps({
      target: { card: scheduler, x: 10, y: 10 },
      onRunNow,
      onClose,
    })
    const menu = renderMenu(props)
    expect(menu.labels()).toContain('topologyCardContextMenu.runNow')
    menu.clickItem('topologyCardContextMenu.runNow')
    expect(onRunNow).toHaveBeenCalledWith(scheduler)
    expect(onClose).toHaveBeenCalled()
    menu.cleanup()

    // Non-scheduler cards hide the item even when the callback is provided.
    const plain = makeCard('MapA', { tags: [] })
    const props2 = baseProps({
      target: { card: plain, x: 10, y: 10 },
      onRunNow: vi.fn(),
    })
    const menu2 = renderMenu(props2)
    expect(menu2.labels()).not.toContain('topologyCardContextMenu.runNow')
    menu2.cleanup()
  })
})

describe('TopologyCardContextMenu fold/unfold item', () => {
  afterEach(() => { document.body.innerHTML = '' })

  it('shows Fold Child Cards for workflow cards in topology context and fires onToggleFold', () => {
    const onToggleFold = vi.fn()
    const wf = makeCard('MapA', { data: {} })
    const props = baseProps({ target: { card: wf, x: 10, y: 10 }, onToggleFold })
    const menu = renderMenu(props)
    expect(menu.labels()).toContain('topologyCardContextMenu.foldChildren')
    menu.clickItem('topologyCardContextMenu.foldChildren')
    expect(onToggleFold).toHaveBeenCalledWith(wf)
    menu.cleanup()
  })

  it('hides the fold item for non-workflow cards and in workflow context', () => {
    const onToggleFold = vi.fn()
    const task = makeCard('TaskA', { type: 'task', data: {} })
    const menu = renderMenu(baseProps({ target: { card: task, x: 10, y: 10 }, onToggleFold }))
    expect(menu.labels()).not.toContain('topologyCardContextMenu.foldChildren')
    menu.cleanup()

    const wf = makeCard('MapA', { data: {} })
    const menu2 = renderMenu(baseProps({
      target: { card: wf, x: 10, y: 10 },
      context: 'workflow',
      onToggleFold,
    }))
    expect(menu2.labels()).not.toContain('topologyCardContextMenu.foldChildren')
    expect(menu2.labels()).not.toContain('topologyCardContextMenu.unfoldChildren')
    menu2.cleanup()
  })

  it('labels the item Unfold when the map id is in foldedWorkflows', () => {
    const wf = makeCard('MapA', { data: {} })
    const props = baseProps({
      target: { card: wf, x: 10, y: 10 },
      onToggleFold: vi.fn(),
      foldedWorkflows: new Set(['MapA']),
    })
    const menu = renderMenu(props)
    expect(menu.labels()).toContain('topologyCardContextMenu.unfoldChildren')
    expect(menu.labels()).not.toContain('topologyCardContextMenu.foldChildren')
    menu.cleanup()
  })
})