import { describe, it, expect } from 'vitest'
import type { Element, Root } from 'hast'
import { langFromPath, isLangSupported, highlightCode } from './diff-highlight'

function classNames(root: Root): string[] {
  const out: string[] = []
  const walk = (node: Root | Element | { type: string; children?: unknown[] }): void => {
    const anyNode = node as { type: string; properties?: { className?: unknown }; children?: unknown[] }
    if (anyNode.type === 'element') {
      const c = anyNode.properties?.className
      if (Array.isArray(c)) out.push(...(c as string[]))
      else if (typeof c === 'string') out.push(c)
    }
    const children = anyNode.children
    if (Array.isArray(children)) for (const child of children) walk(child as Element)
  }
  walk(root)
  return out
}

describe('diff-highlight.langFromPath', () => {
  it('maps common extensions to lowlight languages', () => {
    expect(langFromPath('main.go')).toBe('go')
    expect(langFromPath('app.ts')).toBe('typescript')
    expect(langFromPath('App.tsx')).toBe('typescript')
    expect(langFromPath('index.js')).toBe('javascript')
    expect(langFromPath('style.css')).toBe('css')
    expect(langFromPath('theme.scss')).toBe('css')
    expect(langFromPath('data.json')).toBe('json')
    expect(langFromPath('run.sh')).toBe('bash')
    expect(langFromPath('page.html')).toBe('xml')
    expect(langFromPath('logo.svg')).toBe('xml')
    expect(langFromPath('query.sql')).toBe('sql')
    expect(langFromPath('config.yml')).toBe('yaml')
    expect(langFromPath('README.md')).toBe('markdown')
    expect(langFromPath('main.rs')).toBe('rust')
    expect(langFromPath('Main.java')).toBe('java')
    expect(langFromPath('util.cpp')).toBe('cpp')
    expect(langFromPath('app.rb')).toBe('ruby')
    expect(langFromPath('index.php')).toBe('php')
  })

  it('handles windows-style paths and case-insensitive extensions', () => {
    expect(langFromPath('src\\pkg\\File.GO')).toBe('go')
  })

  it('returns null for unknown extensions and missing paths', () => {
    expect(langFromPath('archive.zip')).toBeNull()
    expect(langFromPath('noext')).toBeNull()
    expect(langFromPath('')).toBeNull()
    expect(langFromPath(undefined)).toBeNull()
    expect(langFromPath(null)).toBeNull()
  })
})

describe('diff-highlight.isLangSupported', () => {
  it('reports registered languages as supported', () => {
    expect(isLangSupported('go')).toBe(true)
    expect(isLangSupported('typescript')).toBe(true)
  })

  it('rejects unknown or empty languages', () => {
    expect(isLangSupported('brainfuck')).toBe(false)
    expect(isLangSupported(null)).toBe(false)
    expect(isLangSupported('')).toBe(false)
  })
})

describe('diff-highlight.highlightCode', () => {
  it('tokenizes Go keywords and strings into hljs classes', () => {
    const root = highlightCode('go', 'func main() { s := "hi" }')
    const cls = classNames(root)
    expect(cls).toContain('hljs-keyword')
    expect(cls).toContain('hljs-string')
  })

  it('tokenizes spore via the registered custom grammar', () => {
    expect(langFromPath('main.spore')).toBe('spore')
    expect(isLangSupported('spore')).toBe(true)
    expect(isLangSupported('sporescript')).toBe(true)
    const root = highlightCode('spore', 'fun run(x: int): int {\n  // note\n  var s = "hi"\n  return x + 1\n}')
    const cls = classNames(root)
    expect(cls).toContain('hljs-keyword')
    expect(cls).toContain('hljs-comment')
    expect(cls).toContain('hljs-string')
    expect(cls).toContain('hljs-number')
    expect(cls).toContain('hljs-type')
  })

  it('tokenizes TypeScript', () => {
    const root = highlightCode('typescript', 'const x: number = 42')
    const cls = classNames(root)
    expect(cls).toContain('hljs-keyword')
    expect(cls).toContain('hljs-number')
  })

  it('tokenizes CSS selectors', () => {
    const root = highlightCode('css', '.foo { color: red; }')
    const cls = classNames(root)
    expect(cls.some((c) => c.startsWith('hljs-selector') || c === 'hljs-attribute' || c === 'hljs-keyword')).toBe(true)
  })
})
