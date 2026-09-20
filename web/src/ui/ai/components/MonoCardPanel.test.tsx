import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MonoCardPanel } from './MonoCardPanel'
import { I18nProvider } from '../../../i18n'
import type { MonoCard, MonoCardListItem } from '../../../domain/mono-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const { mockStore, mockState, mockLoaded } = vi.hoisted(() => {
  const state = {
    projectId: 'project-1',
    cards: [] as MonoCardListItem[],
    openCards: [] as string[],
    loading: false,
    error: null as string | null,
    cardRefreshKey: {} as Record<string, number>,
  }
  const loaded: { card: MonoCard | null } = { card: null }
  const store = {
    setProjectId: vi.fn(),
    load: vi.fn().mockResolvedValue(undefined),
    getCard: vi.fn(async () => loaded.card),
    updateCard: vi.fn().mockResolvedValue(undefined),
    deleteCard: vi.fn().mockResolvedValue(undefined),
    closeCard: vi.fn().mockResolvedValue(undefined),
    validateCard: vi.fn().mockResolvedValue({ Valid: true, Errors: [] }),
    triggerTimerCard: vi.fn().mockResolvedValue(undefined),
    subscribe: vi.fn(() => () => {}),
    getState: () => state,
  }
  return { mockStore: store, mockState: state, mockLoaded: loaded }
})

vi.mock('../../panels/mono-store', () => ({ monoStore: mockStore }))

const { mockUseMonoStore, mockDetailProps } = vi.hoisted(() => ({
  mockUseMonoStore: vi.fn(() => mockState),
  mockDetailProps: {
    isEditing: false,
    singleExitEdit: false,
    onBodyChange: (_id: string, _body: string) => {},
  },
}))

vi.mock('../hooks/useMonoStore', () => ({ useMonoStore: mockUseMonoStore }))

vi.mock('./MonoCard', () => ({
  MonoCardDetail: (props: {
    card: MonoCard
    isEditing?: boolean
    singleExitEdit?: boolean
    onBodyChange: (id: string, body: string) => void
    onDelete: (id: string) => void
  }) => {
    mockDetailProps.isEditing = !!props.isEditing
    mockDetailProps.singleExitEdit = !!props.singleExitEdit
    mockDetailProps.onBodyChange = props.onBodyChange
    return (
      <div data-testid="mono-card-detail">
        <button data-testid="panel-delete" onClick={() => props.onDelete(props.card.id)}>delete</button>
      </div>
    )
  },
  DraftCardEditor: () => null,
}))

const workflowCard: MonoCard = {
  id: 'wf-1',
  type: 'workflow',
  tags: [],
  list: [],
  created: '2026-01-01T00:00:00Z',
  modified: '2026-01-01T00:00:00Z',
  body: '',
  raw: '',
  data: { scope: { include: ['task-1', 'task-2'] } },
}

const taskCard: MonoCard = {
  id: 'task-1',
  type: 'task',
  status: 'todo',
  tags: [],
  list: [],
  created: '2026-01-01T00:00:00Z',
  modified: '2026-01-01T00:00:00Z',
  body: '',
  raw: '',
}

function listItem(id: string, type: string): MonoCardListItem {
  return { id, type, tags: [], list: [], created: '2026-01-01T00:00:00Z', modified: '2026-01-01T00:00:00Z' }
}

describe('MonoCardPanel delete confirmation', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mockState.cards = [listItem('wf-1', 'workflow'), listItem('task-1', 'task'), listItem('task-2', 'task')]
    mockLoaded.card = null
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  async function renderPanel(cardId: string) {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <MonoCardPanel cardId={cardId} projectId="project-1" />
        </I18nProvider>,
      )
    })
  }

  function clickDeleteButton() {
    const btn = container.querySelector<HTMLButtonElement>('[data-testid="panel-delete"]')
    if (!btn) throw new Error('delete button not rendered')
    act(() => { btn.click() })
  }

  function dialogTitle(): string {
    return container.querySelector('.confirm-dialog-title')?.textContent ?? ''
  }

  function confirmDialog() {
    const btn = container.querySelector<HTMLButtonElement>('.confirm-dialog-btn.confirm')
    if (!btn) throw new Error('confirm button not rendered')
    act(() => { btn.click() })
  }

  it('deleting a workflow card warns and cascades to its scoped tasks', async () => {
    mockLoaded.card = workflowCard
    await renderPanel('wf-1')
    clickDeleteButton()

    expect(dialogTitle()).toBe('删除整个工作流')
    const desc = container.querySelector('.confirm-dialog-desc')?.textContent ?? ''
    expect(desc).toContain('wf-1')
    expect(desc).toContain('2')

    await act(async () => { confirmDialog() })
    await act(async () => { await Promise.resolve() })

    expect(mockStore.deleteCard.mock.calls.map(c => c[0])).toEqual(['task-1', 'task-2', 'wf-1'])
    expect(mockStore.closeCard).toHaveBeenCalledWith('wf-1', undefined)
    expect(container.textContent).toContain('Card deleted')
  })

  it('deleting a task card warns and deletes only that card', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    clickDeleteButton()

    expect(dialogTitle()).toBe('删除任务卡片')

    await act(async () => { confirmDialog() })
    await act(async () => { await Promise.resolve() })

    expect(mockStore.deleteCard.mock.calls.map(c => c[0])).toEqual(['task-1'])
  })

  it('cancelling the delete dialog keeps the card', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    clickDeleteButton()

    const cancel = container.querySelector<HTMLButtonElement>('.confirm-dialog-btn.cancel')
    expect(cancel).toBeTruthy()
    act(() => { cancel!.click() })

    expect(mockStore.deleteCard).not.toHaveBeenCalled()
    expect(container.querySelector('[data-testid="panel-delete"]')).toBeTruthy()
  })
})

describe('MonoCardPanel initialEditMode', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mockState.cards = [listItem('wf-1', 'workflow'), listItem('task-1', 'task')]
    mockLoaded.card = null
    mockDetailProps.isEditing = false
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  async function renderPanel(cardId: string, initialEditMode?: boolean) {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <MonoCardPanel cardId={cardId} projectId="project-1" initialEditMode={initialEditMode} />
        </I18nProvider>,
      )
    })
  }

  function isEditing(): boolean {
    return mockDetailProps.isEditing
  }

  it('opens a card in edit mode when initialEditMode is true', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1', true)
    expect(isEditing()).toBe(true)
  })

  it('opens a card in view mode when initialEditMode is false/undefined', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    expect(isEditing()).toBe(false)
  })

  it('switching cards returns to view mode', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1', true)
    expect(isEditing()).toBe(true)

    mockLoaded.card = workflowCard
    await renderPanel('wf-1')
    expect(isEditing()).toBe(false)
  })

  it('enters edit mode when the tab payload requests edit for an already-open card', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    expect(isEditing()).toBe(false)

    // Same card, but the tab payload now requests edit mode (AIShellLayout updates payload.edit).
    await renderPanel('task-1', true)
    expect(isEditing()).toBe(true)
  })
})

describe('MonoCardPanel double-click-to-edit', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mockState.projectId = 'project-1'
    mockState.cards = [listItem('task-1', 'task')]
    mockLoaded.card = null
    mockDetailProps.isEditing = false
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  async function renderPanel(cardId: string, initialEditMode?: boolean) {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <MonoCardPanel cardId={cardId} projectId="project-1" initialEditMode={initialEditMode} />
        </I18nProvider>,
      )
    })
  }

  function fireDblClick(el: Element) {
    el.dispatchEvent(new MouseEvent('dblclick', { bubbles: true, cancelable: true }))
  }

  function isEditing(): boolean {
    return mockDetailProps.isEditing
  }

  it('double-clicking the panel in view mode enters edit mode', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    expect(isEditing()).toBe(false)
    act(() => { fireDblClick(container.querySelector('.mono-card-panel')!) })
    expect(isEditing()).toBe(true)
  })

  it('double-clicking an interactive element does not enter edit mode', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    // The mock's only interactive element (delete button) must be ignored:
    // dispatch the dblclick on a synthetic anchor that the panel would receive.
    const btn = container.querySelector<HTMLButtonElement>('[data-testid="panel-delete"]')!
    act(() => { fireDblClick(btn) })
    expect(isEditing()).toBe(false)
  })

  it('double-clicking while already editing does not re-enter or reset', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1', true)
    expect(isEditing()).toBe(true)
    act(() => { fireDblClick(container.querySelector('.mono-card-panel')!) })
    expect(isEditing()).toBe(true)
  })
})

describe('MonoCardPanel Esc-to-exit + single exit button', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mockState.projectId = 'project-1'
    mockState.cards = [listItem('task-1', 'task')]
    mockLoaded.card = null
    mockDetailProps.isEditing = false
    mockDetailProps.singleExitEdit = false
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  async function renderPanel(cardId: string, initialEditMode?: boolean) {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <MonoCardPanel cardId={cardId} projectId="project-1" initialEditMode={initialEditMode} />
        </I18nProvider>,
      )
    })
  }

  function fireKeydown(key: string, opts: { preventDefault?: boolean } = {}) {
    const ev = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
    if (opts.preventDefault) ev.preventDefault()
    document.dispatchEvent(ev)
  }

  function isEditing(): boolean {
    return mockDetailProps.isEditing
  }

  it('Esc while editing flushes pending edits and exits edit mode', async () => {
    mockLoaded.card = { ...taskCard, body: 'old body' }
    await renderPanel('task-1', true)
    expect(isEditing()).toBe(true)

    // Simulate a body edit buffered through the detail editor, then Esc.
    act(() => { mockDetailProps.onBodyChange('task-1', 'new body') })
    act(() => { fireKeydown('Escape') })
    await act(async () => { await Promise.resolve() })

    expect(mockStore.updateCard).toHaveBeenCalledWith(
      'task-1',
      expect.objectContaining({ id: 'task-1', body: 'new body' }),
      undefined,
    )
    expect(isEditing()).toBe(false)
  })

  it('Esc exits edit mode without a store write when nothing changed', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1', true)
    expect(isEditing()).toBe(true)

    act(() => { fireKeydown('Escape') })
    await act(async () => { await Promise.resolve() })

    expect(mockStore.updateCard).not.toHaveBeenCalled()
    expect(isEditing()).toBe(false)
  })

  it('Esc in view mode does nothing', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    expect(isEditing()).toBe(false)

    act(() => { fireKeydown('Escape') })
    expect(isEditing()).toBe(false)
    expect(mockStore.updateCard).not.toHaveBeenCalled()
  })

  it('a defaultPrevented Escape (handled by an inner editor) is ignored', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1', true)
    expect(isEditing()).toBe(true)

    act(() => { fireKeydown('Escape', { preventDefault: true }) })
    expect(isEditing()).toBe(true)
  })

  it('passes singleExitEdit to the card detail (single exit button mode)', async () => {
    mockLoaded.card = taskCard
    await renderPanel('task-1')
    expect(mockDetailProps.singleExitEdit).toBe(true)
  })
})

describe('MonoCardPanel foreign-project card (tab kept open across a project switch)', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    // The singleton store sits on project-1 while the tab's card belongs to
    // project-2 (opened before the shell switched projects).
    mockState.projectId = 'project-1'
    mockState.cards = []
    mockLoaded.card = null
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('fetches with an explicit projectId instead of showing Card not found', async () => {
    mockLoaded.card = taskCard
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <MonoCardPanel cardId="task-1" projectId="project-2" />
        </I18nProvider>,
      )
    })
    expect(mockStore.getCard).toHaveBeenCalledWith('task-1', { projectId: 'project-2' })
    expect(container.querySelector('[data-testid="mono-card-detail"]')).toBeTruthy()
    expect(container.textContent).not.toContain('Card not found')
  })

  it('never hijacks the singleton store to the foreign project', async () => {
    mockLoaded.card = taskCard
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <MonoCardPanel cardId="task-1" projectId="project-2" />
        </I18nProvider>,
      )
    })
    expect(mockStore.setProjectId).not.toHaveBeenCalled()
    expect(mockStore.load).not.toHaveBeenCalled()
  })

  it('exposes data-card-id on the outer panel container', async () => {
    mockLoaded.card = taskCard
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="zh-CN">
          <MonoCardPanel cardId="task-1" projectId="project-2" />
        </I18nProvider>,
      )
    })
    const panel = container.querySelector('.mono-card-panel')
    expect(panel?.getAttribute('data-card-id')).toBe('task-1')
  })
})
