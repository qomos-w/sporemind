import { describe, it, expect, vi } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MonoCardCompact } from './MonoCardCompact'
import type { MonoCardListItem } from '../../../domain/mono-types'

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: 'zh-CN' }),
}))

vi.mock('./CardAgentAvatar', () => ({
  CardAgentAvatar: () => null,
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function render(card: Partial<MonoCardListItem> & { id: string }): HTMLElement {
  const container = document.createElement('div')
  document.body.appendChild(container)
  let root: Root | null = createRoot(container)
  act(() => {
    root!.render(
      <MonoCardCompact
        card={{ type: 'task', tags: [], data: {}, ...card } as MonoCardListItem}
      />,
    )
  })
  return container
}

describe('MonoCardCompact category bar', () => {
  it('renders the category color bar for a task card with data.category', () => {
    const container = render({ id: 't1', data: { category: 'research' } })
    const bar = container.querySelector('.mono-card-compact-category-bar') as HTMLElement | null
    expect(bar).not.toBeNull()
    expect(bar!.style.background).toBe('#6366f1')
    container.remove()
  })

  it('renders a different color for a code task', () => {
    const container = render({ id: 't2', data: { category: 'code' } })
    const bar = container.querySelector('.mono-card-compact-category-bar') as HTMLElement | null
    expect(bar).not.toBeNull()
    expect(bar!.style.background).toBe('var(--accent-primary)')
    container.remove()
  })

  it('renders no category bar without a known category', () => {
    const container = render({ id: 't3', data: {} })
    expect(container.querySelector('.mono-card-compact-category-bar')).toBeNull()
    container.remove()
  })
})
