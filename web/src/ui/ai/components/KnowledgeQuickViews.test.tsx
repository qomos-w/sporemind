import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import {
  KnowledgeHomeView,
  KnowledgeStarredView,
  KnowledgeSearchView,
} from './KnowledgeQuickViews'
import type { KbQuickViewsDataSource } from './kbQuickViews.data'
import { makeCardRef, type KbProjectRef, type KbSearchHit } from './kbQuickViews.logic'

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: 'en-US' }),
}))

// Every case injects its own stub source, so the production data channel (and
// its generated-client / `@qomos/*` import graph) is cut out entirely.
vi.mock('./kbQuickViews.data', () => ({
  defaultKbQuickViewsDataSource: {
    listProjects: async () => [],
    listStarred: async () => [],
    listRecent: async () => [],
    search: async () => [],
    setStarred: async () => {},
  },
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const projects: KbProjectRef[] = [
  { projectId: 'p1', projectName: 'Alpha' },
  { projectId: 'p2', projectName: 'Beta' },
]
const starred = [makeCardRef('p1', 'Alpha', 'design'), makeCardRef('p2', 'Beta', 'notes')]
const recent = [makeCardRef('p2', 'Beta', 'log')]
const hits: KbSearchHit[] = [
  { projectId: 'p1', projectName: 'Alpha', cardId: 'design', line: 3, snippet: 'hello world' },
  { projectId: 'p2', projectName: 'Beta', cardId: 'notes' },
]

function stubSource(partial: Partial<KbQuickViewsDataSource> = {}): KbQuickViewsDataSource {
  return {
    listProjects: async () => [],
    listStarred: async () => [],
    listRecent: async () => [],
    search: async () => [],
    setStarred: async () => {},
    ...partial,
  }
}

function mount(ui: React.ReactElement): { container: HTMLDivElement; root: Root } {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => { root.render(ui) })
  return { container, root }
}

function click(el: Element | null) {
  expect(el).not.toBeNull()
  act(() => { fireEvent.click(el!) })
}

async function flush() {
  await act(async () => {
    await new Promise(resolve => setTimeout(resolve, 0))
  })
}

describe('KnowledgeHomeView', () => {
  let mounted: { container: HTMLDivElement; root: Root } | null = null

  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => {
    if (mounted) { act(() => mounted!.root.unmount()); mounted.container.remove(); mounted = null }
  })

  const homeSource = () =>
    stubSource({
      listProjects: async () => projects,
      listStarred: async () => starred,
      listRecent: async () => recent,
    })

  it('renders a starred, a recent and a project section', async () => {
    mounted = mount(<KnowledgeHomeView dataSource={homeSource()} />)
    await flush()
    const { container } = mounted
    expect(container.querySelector('.kb-quick-view--home')).not.toBeNull()
    expect(container.querySelectorAll('.kb-quick-section')).toHaveLength(3)
    expect(container.querySelector('li[data-card-id="design"]')?.textContent).toContain('Alpha')
    expect(container.querySelector('li[data-card-id="log"]')?.textContent).toContain('Beta')
  })

  it('opens a project swimlane when a project entry is clicked', async () => {
    const onOpenProject = vi.fn()
    mounted = mount(<KnowledgeHomeView dataSource={homeSource()} onOpenProject={onOpenProject} />)
    await flush()
    click(mounted.container.querySelector('[data-project-id="p2"] .kb-quick-item-main--project'))
    expect(onOpenProject).toHaveBeenCalledTimes(1)
    expect(onOpenProject.mock.calls[0]![0]).toEqual(projects[1])
  })

  it('opens a card swimlane when a starred/recent card is clicked', async () => {
    const onOpenCard = vi.fn()
    mounted = mount(<KnowledgeHomeView dataSource={homeSource()} onOpenCard={onOpenCard} />)
    await flush()
    click(mounted.container.querySelector('li[data-card-id="design"] .kb-quick-item-main'))
    expect(onOpenCard).toHaveBeenCalledWith(starred[0])
  })

  it('offers a see-all affordance that switches to the starred view', async () => {
    const onSelectView = vi.fn()
    mounted = mount(<KnowledgeHomeView dataSource={homeSource()} onSelectView={onSelectView} />)
    await flush()
    click(mounted.container.querySelector('.kb-quick-section-action'))
    expect(onSelectView).toHaveBeenCalledWith('starred')
  })

  it('renders per-section empty states', async () => {
    mounted = mount(<KnowledgeHomeView dataSource={stubSource()} />)
    await flush()
    const text = mounted.container.textContent ?? ''
    expect(text).toContain('knowledgeQuickViews.home.starredEmpty')
    expect(text).toContain('knowledgeQuickViews.home.recentEmpty')
    expect(text).toContain('knowledgeQuickViews.home.projectsEmpty')
  })
})

describe('KnowledgeStarredView', () => {
  let mounted: { container: HTMLDivElement; root: Root } | null = null

  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => {
    if (mounted) { act(() => mounted!.root.unmount()); mounted.container.remove(); mounted = null }
  })

  it('lists every starred card across projects with its project name', async () => {
    mounted = mount(<KnowledgeStarredView dataSource={stubSource({ listStarred: async () => starred })} />)
    await flush()
    const { container } = mounted
    expect(container.querySelectorAll('.kb-quick-item')).toHaveLength(2)
    expect(container.querySelector('li[data-card-id="design"]')?.textContent).toContain('Alpha')
    expect(container.querySelector('li[data-card-id="notes"]')?.textContent).toContain('Beta')
  })

  it('opens the card swimlane on click', async () => {
    const onOpenCard = vi.fn()
    mounted = mount(
      <KnowledgeStarredView
        dataSource={stubSource({ listStarred: async () => starred })}
        onOpenCard={onOpenCard}
      />,
    )
    await flush()
    click(mounted.container.querySelector('li[data-card-id="notes"] .kb-quick-item-main'))
    expect(onOpenCard).toHaveBeenCalledWith(starred[1])
  })

  it('un-stars a card in place and drops the row', async () => {
    const setStarred = vi.fn(async () => {})
    const onUnstar = vi.fn()
    mounted = mount(
      <KnowledgeStarredView
        dataSource={stubSource({ listStarred: async () => starred, setStarred })}
        onUnstar={onUnstar}
      />,
    )
    await flush()
    click(mounted.container.querySelector('li[data-card-id="design"] .kb-quick-item-tail'))
    await flush()
    expect(setStarred).toHaveBeenCalledWith('p1', 'design', false)
    expect(onUnstar).toHaveBeenCalledWith(starred[0])
    expect(mounted.container.querySelector('li[data-card-id="design"]')).toBeNull()
    expect(mounted.container.querySelector('li[data-card-id="notes"]')).not.toBeNull()
  })

  it('shows an empty state when nothing is starred', async () => {
    mounted = mount(<KnowledgeStarredView dataSource={stubSource()} />)
    await flush()
    expect(mounted.container.textContent).toContain('knowledgeQuickViews.starred.empty')
  })
})

describe('KnowledgeSearchView', () => {
  let mounted: { container: HTMLDivElement; root: Root } | null = null

  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => {
    if (mounted) { act(() => mounted!.root.unmount()); mounted.container.remove(); mounted = null }
  })

  const type = (container: HTMLElement, value: string) => {
    act(() => {
      fireEvent.change(container.querySelector('.kb-quick-search-input')!, { target: { value } })
    })
  }

  it('prompts for a query before searching', () => {
    mounted = mount(<KnowledgeSearchView dataSource={stubSource()} debounceMs={0} />)
    expect(mounted.container.textContent).toContain('knowledgeQuickViews.search.prompt')
  })

  it('searches across projects and renders hits with their project names', async () => {
    const listProjects = vi.fn(async () => projects)
    const search = vi.fn(async () => hits)
    mounted = mount(
      <KnowledgeSearchView dataSource={stubSource({ listProjects, search })} debounceMs={0} />,
    )
    type(mounted.container, 'des')
    await flush()
    await flush()
    expect(listProjects).toHaveBeenCalledTimes(1)
    expect(search).toHaveBeenCalledWith('des', projects, undefined)
    const { container } = mounted
    expect(container.querySelector('li[data-card-id="design"]')?.textContent).toContain('hello world')
    expect(container.querySelector('li[data-card-id="design"]')?.textContent).toContain('Alpha')
    expect(container.querySelector('.kb-quick-search-count')?.textContent).toContain(
      'knowledgeQuickViews.search.resultCount',
    )
  })

  it('opens the card swimlane when a result is clicked', async () => {
    const onOpenCard = vi.fn()
    mounted = mount(
      <KnowledgeSearchView
        dataSource={stubSource({ listProjects: async () => projects, search: async () => hits })}
        debounceMs={0}
        onOpenCard={onOpenCard}
      />,
    )
    type(mounted.container, 'des')
    await flush()
    await flush()
    click(mounted.container.querySelector('li[data-card-id="design"] .kb-quick-item-main'))
    expect(onOpenCard).toHaveBeenCalledWith(hits[0])
  })

  it('shows an empty state when nothing matches', async () => {
    mounted = mount(
      <KnowledgeSearchView
        dataSource={stubSource({ listProjects: async () => projects, search: async () => [] })}
        debounceMs={0}
      />,
    )
    type(mounted.container, 'zzz')
    await flush()
    await flush()
    expect(mounted.container.textContent).toContain('knowledgeQuickViews.search.empty')
  })

  it('surfaces search failures', async () => {
    mounted = mount(
      <KnowledgeSearchView
        dataSource={stubSource({
          listProjects: async () => projects,
          search: async () => {
            throw new Error('boom')
          },
        })}
        debounceMs={0}
      />,
    )
    type(mounted.container, 'des')
    await flush()
    await flush()
    expect(mounted.container.textContent).toContain('knowledgeQuickViews.search.error')
  })

  it('clears the query and returns to the prompt', async () => {
    mounted = mount(
      <KnowledgeSearchView
        dataSource={stubSource({ listProjects: async () => projects, search: async () => hits })}
        debounceMs={0}
      />,
    )
    type(mounted.container, 'des')
    await flush()
    await flush()
    click(mounted.container.querySelector('.kb-quick-search-clear'))
    await flush()
    expect(mounted.container.textContent).toContain('knowledgeQuickViews.search.prompt')
    expect(mounted.container.querySelector('li[data-card-id="design"]')).toBeNull()
  })
})
