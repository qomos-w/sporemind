import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import React from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { fireEvent } from '@testing-library/react'
import { KnowledgeSwimlane } from './KnowledgeSwimlane'
import type { LaneStackCard } from './knowledgeMode.logic'

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: 'zh-CN' }),
}))

// `parts/wikiword` pulls in the file-reference plugin, whose client imports
// resolve through the host's file: dependencies (`@qomos/*`). The stack only
// needs the pure wikiword processing, so stub the file-reference leaf out.
vi.mock('./parts/file-reference', () => ({
  isCodeFileExt: () => false,
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const card = (id: string, depth = 0, extra: Partial<LaneStackCard> = {}): LaneStackCard => ({
  id,
  depth,
  tags: [],
  list: [],
  modified: '',
  ...extra,
})

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

const cardEl = (container: HTMLElement, id: string) => container.querySelector(`[data-card-id="${id}"]`) as HTMLElement | null
const isCollapsed = (container: HTMLElement, id: string) => cardEl(container, id)?.classList.contains('kb-swimlane-card--collapsed') ?? false
const toggleBtn = (container: HTMLElement, id: string) => cardEl(container, id)!.querySelector('.kb-swimlane-card-toggle')!

describe('KnowledgeSwimlane', () => {
  let mounted: { container: HTMLDivElement; root: Root } | null = null

  beforeEach(() => { vi.clearAllMocks() })
  afterEach(() => {
    if (mounted) { act(() => mounted!.root.unmount()); mounted.container.remove(); mounted = null }
  })

  it('renders every stack card and never a toc container card', () => {
    mounted = mount(<KnowledgeSwimlane cards={[card('a'), card('b', 1)]} />)
    const { container } = mounted
    expect(container.querySelectorAll('.kb-swimlane-card')).toHaveLength(2)
    expect(cardEl(container, 'a')).not.toBeNull()
    expect(cardEl(container, 'b')).not.toBeNull()
    expect(cardEl(container, 'toc')).toBeNull()
    expect(cardEl(container, 'b')!.getAttribute('data-depth')).toBe('1')
  })

  it('collapses every card by default and expands exactly one at a time', () => {
    mounted = mount(<KnowledgeSwimlane cards={[card('a'), card('b')]} />)
    const { container } = mounted
    expect(isCollapsed(container, 'a')).toBe(true)
    expect(isCollapsed(container, 'b')).toBe(true)

    click(toggleBtn(container, 'a'))
    expect(isCollapsed(container, 'a')).toBe(false)
    expect(isCollapsed(container, 'b')).toBe(true)

    // expanding b auto-collapses a (accordion)
    click(toggleBtn(container, 'b'))
    expect(isCollapsed(container, 'b')).toBe(false)
    expect(isCollapsed(container, 'a')).toBe(true)

    const expanded = container.querySelectorAll('.kb-swimlane-card:not(.kb-swimlane-card--collapsed)')
    expect(expanded.length).toBe(1)

    // the same button collapses again
    click(toggleBtn(container, 'b'))
    expect(isCollapsed(container, 'b')).toBe(true)
  })

  it('exposes a single icon-only toggle button per card', () => {
    mounted = mount(<KnowledgeSwimlane cards={[card('a')]} />)
    const { container } = mounted
    const actions = container.querySelector('.kb-swimlane-card-actions')
    expect(actions).toBeNull()
    const btn = toggleBtn(container, 'a') as HTMLButtonElement
    expect(btn.textContent?.trim()).toBe('')
    expect(btn.getAttribute('aria-expanded')).toBe('false')
    click(btn)
    expect(btn.getAttribute('aria-expanded')).toBe('true')
  })

  it('renders the collapsed summary line and the full body only when expanded', () => {
    mounted = mount(
      <KnowledgeSwimlane cards={[card('a', 0, { body: '# Heading\n\nBody paragraph' })]} />,
    )
    const { container } = mounted
    expect(container.querySelector('[data-card-id="a"] .kb-swimlane-card-summary')?.textContent).toBe('Heading')
    expect(container.querySelector('[data-card-id="a"] .kb-swimlane-card-body')).toBeNull()

    click(toggleBtn(container, 'a'))
    expect(container.querySelector('[data-card-id="a"] .kb-swimlane-card-body')).not.toBeNull()
    expect(container.querySelector('[data-card-id="a"] .kb-swimlane-card-summary')).toBeNull()
  })

  it('expands a collapsed card when its main row is clicked', () => {
    mounted = mount(<KnowledgeSwimlane cards={[card('a', 0, { body: 'Body' })]} />)
    const { container } = mounted
    expect(isCollapsed(container, 'a')).toBe(true)
    click(cardEl(container, 'a')!.querySelector('.kb-swimlane-card-main'))
    expect(isCollapsed(container, 'a')).toBe(false)
  })

  it('keeps body links clickable without folding the card', () => {
    mounted = mount(
      <KnowledgeSwimlane cards={[card('a', 0, { body: '[docs](https://example.com)' })]} />,
    )
    const { container } = mounted
    click(toggleBtn(container, 'a'))
    const link = container.querySelector('[data-card-id="a"] .kb-swimlane-card-body a')
    expect(link).not.toBeNull()
    click(link)
    expect(isCollapsed(container, 'a')).toBe(false)
  })

  it('shows the empty placeholder for an empty stack', () => {
    mounted = mount(<KnowledgeSwimlane cards={[]} />)
    expect(mounted.container.querySelector('.kb-swimlane-empty')).not.toBeNull()
  })

  it('supports a controlled collapsed-id set and reports changes', () => {
    const onCollapsedIdsChange = vi.fn()
    const collapsed = new Set(['a', 'b'])
    mounted = mount(
      <KnowledgeSwimlane
        cards={[card('a'), card('b')]}
        collapsedIds={collapsed}
        onCollapsedIdsChange={onCollapsedIdsChange}
      />,
    )
    const { container } = mounted
    expect(isCollapsed(container, 'a')).toBe(true)

    click(toggleBtn(container, 'a'))
    expect(onCollapsedIdsChange).toHaveBeenCalledTimes(1)
    const next = onCollapsedIdsChange.mock.calls[0]![0] as Set<string>
    expect(next.has('a')).toBe(false)
    expect(next.has('b')).toBe(true)
  })
})
