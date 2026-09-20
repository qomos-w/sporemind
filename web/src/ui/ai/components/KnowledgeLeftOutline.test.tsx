import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import { KnowledgeLeftOutline } from './KnowledgeLeftOutline'
import type { KbOutlineProject } from './knowledgeLeftOutline.logic'
import type { WikiCardTreeNode } from '../../../gen-clients/system/types'

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: 'zh-CN' }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const treeNode = (id: string, children: WikiCardTreeNode[] = []): WikiCardTreeNode => ({
  Id: id,
  Title: id,
  Type: 'wiki',
  Status: '',
  Modified: '',
  Children: children,
})

const toc = (children: WikiCardTreeNode[]): WikiCardTreeNode[] => [treeNode('toc', children)]

const p1: KbOutlineProject = { projectId: 'p1', name: 'Alpha' }
const p2: KbOutlineProject = { projectId: 'p2', name: 'Beta' }

function mount(ui: React.ReactElement): { container: HTMLDivElement; root: Root } {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => { root.render(ui) })
  return { container, root }
}

function click(el: Element | null | undefined) {
  expect(el).toBeTruthy()
  act(() => { fireEvent.click(el!) })
}

const rows = (container: HTMLElement) => container.querySelectorAll('.kb-outline-row--project')
const projectCaret = (container: HTMLElement, i = 0) => rows(container)[i]?.querySelector('.kb-outline-caret') ?? null
const projectLabel = (container: HTMLElement, i = 0) => rows(container)[i]?.querySelector('.kb-outline-label') ?? null
const projectMore = (container: HTMLElement, i = 0) => rows(container)[i]?.querySelector('.kb-outline-more') ?? null
const cardLabels = (container: HTMLElement) => container.querySelectorAll('.kb-outline-row--card .kb-outline-label')
const menu = (container: HTMLElement) => container.querySelector('.kb-outline-menu')
const menuItems = (container: HTMLElement) => container.querySelectorAll('.kb-outline-menu-item')

describe('KnowledgeLeftOutline', () => {
  let mounted: { container: HTMLDivElement; root: Root } | null = null

  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => {
    if (mounted) { act(() => mounted!.root.unmount()); mounted.container.remove(); mounted = null }
  })

  it('renders the three top entries and reports view selection', () => {
    const onSelectView = vi.fn()
    mounted = mount(<KnowledgeLeftOutline projects={[p1]} activeView="home" onSelectView={onSelectView} />)
    const { container } = mounted
    const views = container.querySelectorAll('.kb-outline-view')
    expect(views).toHaveLength(3)
    expect(views[0]!.classList.contains('active')).toBe(true)

    click(views[1]!)
    expect(onSelectView).toHaveBeenCalledWith('search')
    click(views[2]!)
    expect(onSelectView).toHaveBeenCalledWith('starred')
  })

  it('lists projects and opens a project lane on label click', () => {
    const onOpenProject = vi.fn()
    mounted = mount(<KnowledgeLeftOutline projects={[p1, p2]} onOpenProject={onOpenProject} />)
    const { container } = mounted
    expect(rows(container)).toHaveLength(2)

    click(projectLabel(container, 1))
    expect(onOpenProject).toHaveBeenCalledTimes(1)
    expect(onOpenProject.mock.calls[0]![0].projectId).toBe('p2')
  })

  it('shows an empty note when there are no projects', () => {
    mounted = mount(<KnowledgeLeftOutline projects={[]} />)
    expect(mounted.container.querySelector('.kb-outline-empty')?.textContent).toBe('knowledgeOutline.empty')
  })

  it('lazily requests the TOC the first time a project expands', () => {
    const onRequestProjectToc = vi.fn()
    mounted = mount(
      <KnowledgeLeftOutline projects={[p1]} tocByProject={{}} onRequestProjectToc={onRequestProjectToc} />,
    )
    const { container } = mounted
    click(projectCaret(container))
    expect(onRequestProjectToc).toHaveBeenCalledTimes(1)
    expect(onRequestProjectToc).toHaveBeenCalledWith('p1')
  })

  it('does not re-request a TOC that is already loaded', () => {
    const onRequestProjectToc = vi.fn()
    mounted = mount(
      <KnowledgeLeftOutline
        projects={[p1]}
        tocByProject={{ p1: toc([treeNode('a')]) }}
        onRequestProjectToc={onRequestProjectToc}
      />,
    )
    click(projectCaret(mounted.container))
    expect(onRequestProjectToc).not.toHaveBeenCalled()
  })

  it('renders the TOC tree (toc unwrapped, virtual buckets dropped) and opens a card lane', () => {
    const onOpenCard = vi.fn()
    mounted = mount(
      <KnowledgeLeftOutline
        projects={[p1]}
        expandedProjectIds={new Set(['p1'])}
        tocByProject={{ p1: toc([treeNode('__builtin_task__'), treeNode('card-a')]) }}
        onOpenCard={onOpenCard}
      />,
    )
    const { container } = mounted
    const labels = cardLabels(container)
    expect(labels).toHaveLength(1)
    expect(labels[0]!.textContent).toContain('card-a')

    click(labels[0])
    expect(onOpenCard).toHaveBeenCalledTimes(1)
    expect(onOpenCard.mock.calls[0]![0].projectId).toBe('p1')
    expect(onOpenCard.mock.calls[0]![1]).toBe('card-a')
  })

  it('renders loading, error and empty states', () => {
    mounted = mount(
      <KnowledgeLeftOutline projects={[p1]} expandedProjectIds={new Set(['p1'])} loadingProjectIds={new Set(['p1'])} />,
    )
    expect(mounted.container.querySelector('.kb-outline-note')?.textContent).toBe('knowledgeOutline.loading')
    act(() => mounted!.root.unmount())
    mounted.container.remove()

    mounted = mount(
      <KnowledgeLeftOutline projects={[p1]} expandedProjectIds={new Set(['p1'])} errorByProject={{ p1: 'boom' }} />,
    )
    expect(mounted.container.querySelector('.kb-outline-note--error')?.textContent).toBe('knowledgeOutline.loadError')
    act(() => mounted!.root.unmount())
    mounted.container.remove()

    mounted = mount(
      <KnowledgeLeftOutline projects={[p1]} expandedProjectIds={new Set(['p1'])} tocByProject={{ p1: toc([]) }} />,
    )
    expect(mounted.container.querySelector('.kb-outline-note')?.textContent).toBe('knowledgeOutline.emptyProject')
  })

  it('reports expansion changes for a controlled expansion set', () => {
    const onExpandedProjectIdsChange = vi.fn()
    mounted = mount(
      <KnowledgeLeftOutline
        projects={[p1]}
        expandedProjectIds={new Set()}
        onExpandedProjectIdsChange={onExpandedProjectIdsChange}
      />,
    )
    click(projectCaret(mounted.container))
    expect(onExpandedProjectIdsChange).toHaveBeenCalledTimes(1)
    const next = onExpandedProjectIdsChange.mock.calls[0]![0] as Set<string>
    expect([...next]).toEqual(['p1'])
  })

  it('opens the shared menu from the ⋯ button and opens the project lane', () => {
    const onOpenProject = vi.fn()
    mounted = mount(<KnowledgeLeftOutline projects={[p1]} onOpenProject={onOpenProject} />)
    const { container } = mounted
    expect(menu(container)).toBeNull()

    click(projectMore(container))
    expect(menu(container)).not.toBeNull()
    // project nodes only carry the open item (cards are the starrable nodes)
    expect(menuItems(container)).toHaveLength(1)

    click(menuItems(container)[0])
    expect(onOpenProject).toHaveBeenCalledTimes(1)
    expect(menu(container)).toBeNull()
  })

  it('opens the menu on right-click and stars a card from it', () => {
    const onToggleStar = vi.fn()
    mounted = mount(
      <KnowledgeLeftOutline
        projects={[p1]}
        expandedProjectIds={new Set(['p1'])}
        tocByProject={{ p1: toc([treeNode('card-a')]) }}
        onToggleStar={onToggleStar}
      />,
    )
    const { container } = mounted
    act(() => {
      fireEvent.contextMenu(cardLabels(container)[0]!.closest('.kb-outline-row')!, { clientX: 40, clientY: 60 })
    })
    expect(menu(container)).not.toBeNull()
    expect(menuItems(container)).toHaveLength(2)

    // first item: open; second item: star
    click(menuItems(container)[1])
    expect(onToggleStar).toHaveBeenCalledWith('p1', 'card-a', true)
  })

  it('shows unstar and reports false when the card is already starred', () => {
    const onToggleStar = vi.fn()
    mounted = mount(
      <KnowledgeLeftOutline
        projects={[p1]}
        expandedProjectIds={new Set(['p1'])}
        tocByProject={{ p1: toc([treeNode('card-a')]) }}
        isCardStarred={(projectId, cardId) => projectId === 'p1' && cardId === 'card-a'}
        onToggleStar={onToggleStar}
      />,
    )
    const { container } = mounted
    expect(container.querySelector('.kb-outline-starred')).not.toBeNull()

    // the card's own ⋯ menu carries the unstar action
    click(cardLabels(container)[0]!.closest('.kb-outline-row')!.querySelector('.kb-outline-more'))
    expect(menuItems(container)[1]!.textContent).toContain('knowledgeOutline.menu.unstar')

    click(menuItems(container)[1])
    expect(onToggleStar).toHaveBeenCalledWith('p1', 'card-a', false)
  })

  it('never offers a star action on a project node (no project-level star)', () => {
    const onToggleStar = vi.fn()
    mounted = mount(
      <KnowledgeLeftOutline
        projects={[p1]}
        isCardStarred={() => true}
        onToggleStar={onToggleStar}
      />,
    )
    const { container } = mounted
    click(projectMore(container))
    expect(menuItems(container)).toHaveLength(1)
    expect(menuItems(container)[0]!.textContent).toContain('knowledgeOutline.menu.open')
    // No dead star entry, and no star badge on the project row itself.
    expect(container.querySelector('.kb-outline-row--project .kb-outline-starred')).toBeNull()
    expect(onToggleStar).not.toHaveBeenCalled()
  })
})
