import { describe, expect, it } from 'vitest'
import { cardVisual, getCardVisualStyle } from './cardVisual'

describe('card visual compatibility', () => {
  it('reads semantic visual data', () => {
    expect(cardVisual({ data: { visual: { icon: 'rocket', iconType: 'lucide', accent: 'blue', emphasis: 'strong' } } } as never)).toMatchObject({ icon: 'rocket', iconType: 'lucide', accent: 'blue', emphasis: 'strong' })
  })

  it('keeps legacy colors and identifies legacy glyphs as emoji', () => {
    expect(cardVisual({ data: { visual: { background: '#fff', border: '#000', icon: '★' } } } as never)).toMatchObject({ background: '#fff', border: '#000', icon: '★', iconType: 'emoji' })
  })

  it('derives iconType from the icon value and ignores any stored iconType', () => {
    // Stored iconType contradicts the name; the name wins.
    const emojiForced = cardVisual({ data: { visual: { icon: 'rocket', iconType: 'emoji' } } } as never)
    expect(emojiForced.iconType).toBe('lucide')
    const lucideForced = cardVisual({ data: { visual: { icon: '🚀', iconType: 'lucide' } } } as never)
    expect(lucideForced.iconType).toBe('emoji')
    // No stored iconType at all still routes correctly.
    expect(cardVisual({ data: { visual: { icon: 'file-text' } } } as never).iconType).toBe('lucide')
  })

  it('separates icon color from accent background', () => {
    // Explicit color overrides the accent-derived icon color.
    const withColor = getCardVisualStyle({ accent: 'blue', color: '#dc2626' })
    expect(withColor.icon).toBe('#dc2626')
    // Without an explicit color, the icon falls back to a fixed neutral grey
    // that is intentionally independent of the accent, so switching the
    // background never recolors the icon.
    const noColor = getCardVisualStyle({ accent: 'blue' })
    expect(noColor.icon).not.toBe('#dc2626')
    expect(noColor.icon).toBe('#64748b')
    expect(noColor.icon).not.toBe(noColor.border)
  })

  it('normalises the default slate accent to undefined', () => {
    // 'slate' is the default accent; a stored `accent: 'slate'` (e.g. left
    // over after clearing other visual fields) must read back as undefined so
    // hasVisual checks treat the card as having no custom accent and the
    // topology border falls back to type/agent/status colours.
    expect(cardVisual({ data: { visual: { accent: 'slate' } } } as never).accent).toBeUndefined()
    // A non-default accent is preserved.
    expect(cardVisual({ data: { visual: { accent: 'blue' } } } as never).accent).toBe('blue')
  })

  it('resolves light and dark accent styles', () => {
    const light = getCardVisualStyle({ accent: 'cyan', emphasis: 'strong' }, 'light')
    const dark = getCardVisualStyle({ accent: 'cyan', emphasis: 'strong' }, 'dark')
    expect(light.borderWidth).toBe(2)
    expect(light.background).not.toBe(dark.background)
    expect(light.title).not.toBe(dark.title)
  })

  it('treats empty-string visual fields as absent (backend zero-value fix)', () => {
    // The backend BuiltinComponentCardProvider sends Go zero-value empty
    // strings for unset visual fields (background, border, color). These must
    // not leak through as valid values, or the ?? fallback is bypassed and
    // vis-network renders an empty (black) background.
    const v = cardVisual({ data: { visual: { icon: 'terminal', accent: 'slate', color: '', background: '', border: '' } } } as never)
    expect(v.color).toBeUndefined()
    expect(v.background).toBeUndefined()
    expect(v.border).toBeUndefined()
    const style = getCardVisualStyle({ icon: 'terminal', accent: 'slate' }, 'light')
    // Slate light background is #f8fafc, not empty/black.
    expect(style.background).toBe('#f8fafc')
  })
})
