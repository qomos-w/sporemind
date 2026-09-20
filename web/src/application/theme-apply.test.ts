import { describe, it, expect, beforeEach } from 'vitest'
import { applyTheme, parseThemeJSON } from '@qomos/sporemind-theme'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('applyTheme hue tint (light mode only)', () => {
  beforeEach(() => {
    document.documentElement.removeAttribute('data-theme')
    document.documentElement.removeAttribute('style')
  })

  it('retints the light background primitives at the slider hue', () => {
    applyTheme({ mode: 'light', fontSize: 16, hue: 210 })
    const base = document.documentElement.style.getPropertyValue('--_bg-base')
    expect(base).toMatch(/^hsl\(210 /)
    const elevated = document.documentElement.style.getPropertyValue('--_bg-elevated')
    expect(elevated).toMatch(/^hsl\(210 /)
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  it('removes the tint overrides when switching to dark mode', () => {
    applyTheme({ mode: 'light', fontSize: 16, hue: 210 })
    applyTheme({ mode: 'dark', fontSize: 16, hue: 210 })
    expect(document.documentElement.style.getPropertyValue('--_bg-base')).toBe('')
  })

  it('keeps the neutral palette when hue is absent', () => {
    applyTheme({ mode: 'light', fontSize: 16 })
    expect(document.documentElement.style.getPropertyValue('--_bg-base')).toBe('')
  })

  it('round-trips a tinted theme through JSON parse', () => {
    const theme = parseThemeJSON('{"mode":"light","fontSize":16,"hue":120}')
    applyTheme(theme)
    expect(document.documentElement.style.getPropertyValue('--_bg-base')).toMatch(/^hsl\(120 /)
  })
})
