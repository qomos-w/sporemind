import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { isLucideIconName } from './cardIconLibrary'

// Drift guard: every icon declared by a builtin component card
// (pkg/agentkit/builtin/cards/**) must resolve in the frontend card icon
// library. An unlisted name falls through CardIcon's emoji path and renders
// as the raw identifier — i.e. no icon (2026-09-15: the workbench-attention
// bundle declared `layout-dashboard`, which was missing from the catalog).
// Vitest's cwd is web/, so the repo-rooted card tree is one level up.
const cardsRoot = join(process.cwd(), '..', 'pkg', 'agentkit', 'builtin', 'cards')

function collectMarkdown(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) collectMarkdown(full, out)
    else if (entry.isFile() && entry.name.endsWith('.md')) out.push(full)
  }
  return out
}

/** Icon names in a card's frontmatter: both `data.icon` and
 * `data.visual.icon` (`  icon: x` / `    icon: x`). */
function frontmatterIcons(raw: string): string[] {
  const end = raw.indexOf('\n---', 3)
  const frontmatter = end === -1 ? '' : raw.slice(0, end)
  const icons: string[] = []
  for (const match of frontmatter.matchAll(/^[ \t]+icon:[ \t]*(\S+)[ \t]*$/gm)) {
    icons.push(match[1]!)
  }
  return icons
}

describe('builtin card icon drift', () => {
  it('resolves every builtin card icon in the card icon library', () => {
    const missing: string[] = []
    const files = collectMarkdown(cardsRoot)
    expect(files.length).toBeGreaterThan(20)
    for (const file of files) {
      for (const icon of frontmatterIcons(readFileSync(file, 'utf8'))) {
        if (!isLucideIconName(icon)) missing.push(`${file}: ${icon}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('renders the workbench-attention bundle icon (regression)', () => {
    const raw = readFileSync(join(cardsRoot, 'bundle', 'tool-usage', 'workbench-attention.md'), 'utf8')
    expect(frontmatterIcons(raw)).toContain('layout-dashboard')
    expect(isLucideIconName('layout-dashboard')).toBe(true)
  })
})
