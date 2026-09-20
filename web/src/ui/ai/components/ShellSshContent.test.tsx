import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('ShellSshContent layout', () => {
  it('fills the middle column width via CSS rule', () => {
    const css = readFileSync(resolve(__dirname, 'ShellSshContent.css'), 'utf-8')
    const rootRule = css.match(/\.shell-ssh-content\s*\{[^}]*\}/)?.[0] ?? ''
    expect(rootRule).toContain('flex: 1')
    expect(rootRule).toContain('min-width: 0')
  })

  it('reserves composer space at the bottom of the tree container via CSS rule', () => {
    const css = readFileSync(resolve(__dirname, '../../panels/SshManagerPanel.css'), 'utf-8')
    const listRule = css.match(/\.ssh-tree-container\s*\{[^}]*\}/)?.[0] ?? ''
    expect(listRule).toContain('composer-card-top')
    expect(listRule).toContain('composer-frame-h')
    expect(listRule).toMatch(/padding:.*calc\(/)
  })
})
