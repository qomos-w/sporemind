import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { CARD_ICON_DEFINITIONS, CARD_ICON_LIBRARY } from './cardIconLibrary'

function extractInner(name: string): string {
  const Icon = CARD_ICON_LIBRARY[name]
  if (!Icon) return ''
  const markup = renderToStaticMarkup(<Icon size={24} color="currentColor" strokeWidth={2} />)
  const match = markup.match(/<svg[^>]*>([\s\S]*)<\/svg>/i)
  return match ? match[1] ?? '' : ''
}

describe('card icon library', () => {
  it('renders every icon as a non-empty svg with paths', () => {
    const empties = CARD_ICON_DEFINITIONS
      .filter(def => extractInner(def.name).trim() === '')
      .map(def => def.name)
    expect(empties).toEqual([])
  })

  it('keeps icon names unique', () => {
    const names = CARD_ICON_DEFINITIONS.map(def => def.name)
    expect(new Set(names).size).toBe(names.length)
  })

  // The library zips CARD_ICON_CATALOG with a parallel component array; equal
  // length is enforced at module load. This sentinel test guards against an
  // order swap between the two arrays (same length, wrong pairing).
  it('pairs catalog entries with the intended components', () => {
    const sentinels: Record<string, string> = {
      target: 'Target',
      terminal: 'Terminal',
      smartphone: 'Smartphone',
      'shield-check': 'ShieldCheck',
      watch: 'Watch',
      tv: 'Tv',
      'graduation-cap': 'GraduationCap',
      'layout-dashboard': 'LayoutDashboard',
    }
    for (const [name, displayName] of Object.entries(sentinels)) {
      const Icon = CARD_ICON_LIBRARY[name]
      expect(Icon?.displayName ?? String(Icon?.name ?? ''), `${name} component`).toBe(displayName)
    }
  })

  it('produces a valid node svg data url', () => {
    const inner = extractInner('rocket').replace(/currentColor/gi, '#2563eb')
    const wrapped = `<svg xmlns="http://www.w3.org/2000/svg" width="28" height="28" viewBox="0 0 28 28"><circle cx="14" cy="14" r="14" fill="#eff6ff"/><g transform="translate(2 2)" fill="none" stroke="#2563eb" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${inner}</g></svg>`
    expect(wrapped).toContain('<circle')
    expect(wrapped).toContain('stroke="#2563eb"')
    expect(inner.length).toBeGreaterThan(0)
  })
})
