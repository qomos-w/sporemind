import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MonoCardDetail } from './MonoCard'
import { I18nProvider } from '../../../i18n'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const { mockStore } = vi.hoisted(() => {
  const state: {
    projectId: string | null
    cards: { id: string; title?: string; type?: string; tags: string[] }[]
    openCards: string[]
    loading: boolean
    error: string | null
    cardRefreshKey: Record<string, number>
  } = {
    projectId: 'test',
    cards: [],
    openCards: [],
    loading: false,
    error: null,
    cardRefreshKey: {},
  }
  const listeners = new Set<(s: typeof state) => void>()
  const emit = () => listeners.forEach(l => l(state))

  const store = {
    setProjectId: vi.fn((id: string | null) => {
      state.projectId = id
      emit()
    }),
    load: vi.fn().mockResolvedValue(undefined),
    loadOpenCards: vi.fn().mockResolvedValue([]),
    watchOpenCards: vi.fn().mockResolvedValue(undefined),
    watchCardChanges: vi.fn().mockReturnValue(() => {}),
    watchFileChanges: vi.fn().mockReturnValue(() => {}),
    watchGraphChanges: vi.fn().mockReturnValue(() => {}),
    saveOpenCards: vi.fn().mockResolvedValue(undefined),
    getCard: vi.fn().mockResolvedValue(null),
    openCardAtTop: vi.fn(),
    closeCard: vi.fn(),
    closeAllCards: vi.fn(),
    createCard: vi.fn(),
    updateCard: vi.fn(),
    deleteCard: vi.fn(),
    fetchCardList: vi.fn().mockResolvedValue([]),
    uniqueId: vi.fn().mockReturnValue('draft-1'),
    subscribe: vi.fn((listener: (s: typeof state) => void) => {
      listeners.add(listener)
      listener(state)
      return () => { listeners.delete(listener) }
    }),
    getState: vi.fn(() => state),
  }

  return { mockStore: store, mockState: state, emit }
})

vi.mock('../../panels/mono-store', () => ({
  monoStore: mockStore,
  cardIdFromTitle: (title: string) =>
    title.trim().toLowerCase().replace(/[^a-z0-9\u4e00-\u9fa5]+/g, '-').replace(/^-|-$/g, '') || 'untitled',
}))

describe('MonoCardDetail edit toolbar', () => {
  let container: HTMLDivElement
  let root: Root

  const noop = () => {}
  const card = {
    id: 'card-1', title: 'Card 1', tags: [], list: [],
    created: '', modified: '', body: 'hello', raw: '',
  }

  const renderDetail = async (singleExitEdit?: boolean) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <MonoCardDetail
            card={card}
            isEditing
            editMeta={{ id: 'card-1' }}
            editBody="hello"
            isFolded={false}
            isEntering={false}
            isRemoving={false}
            showTopActions
            singleExitEdit={singleExitEdit}
            onMetaChange={noop}
            onBodyChange={noop}
            onStartEdit={noop}
            onSaveEdit={noop}
            onCancelEdit={noop}
            onDelete={noop}
            onClose={noop}
            onToggleFold={noop}
            onFoldOthers={noop}
            onCloseOthers={noop}
            onClone={noop}
            onNewHere={noop}
            onNewJournalHere={noop}
            onExport={noop}
            onTriggerTimerCard={async () => {}}
            onOpenCard={noop}
            onWikiWord={noop}
            onTagClick={noop}
          />
        </I18nProvider>,
      )
    })
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  function editButtons(): HTMLButtonElement[] {
    const actions = container.querySelector('.mono-card-detail-top-actions')
    if (!actions) throw new Error('top actions not rendered')
    return Array.from(actions.querySelectorAll('button'))
  }

  it('renders the Cancel/Save pair by default', async () => {
    await renderDetail()
    const titles = editButtons().map(b => b.getAttribute('title'))
    expect(titles).toContain('Cancel')
    expect(titles).toContain('Save')
  })

  it('renders a single exit button wired to save when singleExitEdit is set', async () => {
    await renderDetail(true)
    const titles = editButtons().map(b => b.getAttribute('title'))
    expect(titles).not.toContain('Cancel')
    expect(titles).not.toContain('Save')
    expect(titles).toContain('Exit editing')
  })
})
