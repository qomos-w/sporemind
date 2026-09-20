import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Vitest serves modules over a virtual URL, so resolve the stylesheet from the
// project root (the runner's cwd) instead of import.meta.url.
const shellCss = readFileSync(resolve(process.cwd(), 'src/ui/ai/AIShellLayout.css'), 'utf8')

// A bundle color is a mid-tone hex tuned for light surfaces; the dark theme
// lifts exactly the glyphs that carry the marker class emitted by
// resolveAppIcon/componentIcon. jsdom never applies CSS, so pin the rule here.
describe('colored app/bundle glyph dark-theme contrast (stylesheet contract)', () => {
  it('lifts only the marker-classed glyph under the dark theme', () => {
    expect(shellCss).toMatch(/\[data-theme='dark'\] svg\.app-icon-colored \{[\s\S]*?filter: brightness\(/)
  })
})
