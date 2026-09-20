import { describe, it, expect } from 'vitest'
import { componentIcon } from './omniboxLabels'
import type { MonoCardListItem } from '../../../gen-clients/system/types'

// Minimal bundle-card shape: componentIcon only reads Data.visual / Data.icon.
function bundleCard(data: Record<string, unknown>): MonoCardListItem {
  return { Id: 'app-bundle:demo.app:search', Data: data } as unknown as MonoCardListItem
}

describe('componentIcon (omnibox bundle entry)', () => {
  it('tags a colored bundle glyph with the dark-theme contrast marker class', () => {
    const node = componentIcon(bundleCard({ visual: { icon: 'search', color: '#2563eb' } }), null) as any
    expect(node.props.className).toBe('app-icon-colored')
    expect(node.props.color).toBe('#2563eb')
  })

  it('leaves an uncolored bundle glyph untagged', () => {
    const node = componentIcon(bundleCard({ visual: { icon: 'search' } }), null) as any
    expect(node.props.className).toBeUndefined()
  })

  it('returns the fallback when the card declares no icon', () => {
    const fallback = { fallback: true }
    expect(componentIcon(bundleCard({}), fallback as any)).toBe(fallback)
  })
})
