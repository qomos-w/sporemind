import { describe, it, expect } from 'vitest'
import type { Element, Root } from 'hast'
import { parseFileReference, isCodeFileExt, isKnownDotfile, rehypeFileReferences } from './file-reference'

describe('isCodeFileExt', () => {
  it('accepts source-code extensions', () => {
    expect(isCodeFileExt('go')).toBe(true)
    expect(isCodeFileExt('TS')).toBe(true)
    expect(isCodeFileExt('tsx')).toBe(true)
  })

  it('accepts text and config extensions', () => {
    expect(isCodeFileExt('txt')).toBe(true)
    expect(isCodeFileExt('ini')).toBe(true)
    expect(isCodeFileExt('cfg')).toBe(true)
    expect(isCodeFileExt('conf')).toBe(true)
    expect(isCodeFileExt('env')).toBe(true)
    expect(isCodeFileExt('properties')).toBe(true)
  })

  it('accepts markdown extensions', () => {
    expect(isCodeFileExt('md')).toBe(true)
    expect(isCodeFileExt('mdx')).toBe(true)
  })

  describe('isKnownDotfile', () => {
    it('accepts common dotfiles', () => {
      expect(isKnownDotfile('.gitignore')).toBe(true)
      expect(isKnownDotfile('.editorconfig')).toBe(true)
      expect(isKnownDotfile('.eslintrc')).toBe(true)
      expect(isKnownDotfile('.env.local')).toBe(true)
    })

    it('rejects unknown dotfiles', () => {
      expect(isKnownDotfile('.foobar')).toBe(false)
      expect(isKnownDotfile('.secret')).toBe(false)
    })
  })
})

describe('parseFileReference', () => {
  it('parses basename with line range', () => {
    expect(parseFileReference('turn_engine_execute.go:549-560')).toEqual({
      raw: 'turn_engine_execute.go:549-560',
      path: 'turn_engine_execute.go',
      ext: 'go',
      line: 549,
      lineEnd: 560,
    })
  })

  it('parses relative path with line range', () => {
    expect(parseFileReference('pkg/actor/agent/turn_engine_execute.go:12-20')).toEqual({
      raw: 'pkg/actor/agent/turn_engine_execute.go:12-20',
      path: 'pkg/actor/agent/turn_engine_execute.go',
      ext: 'go',
      line: 12,
      lineEnd: 20,
    })
  })

  it('treats reversed range as single line', () => {
    expect(parseFileReference('agent.go:50-42')).toEqual({
      raw: 'agent.go:50-42',
      path: 'agent.go',
      ext: 'go',
      line: 50,
    })
  })

  it('parses basename with line number', () => {
    expect(parseFileReference('turn_engine_execute.go:549')).toEqual({
      raw: 'turn_engine_execute.go:549',
      path: 'turn_engine_execute.go',
      ext: 'go',
      line: 549,
    })
  })

  it('parses relative path with line number', () => {
    expect(parseFileReference('pkg/actor/agent/turn_engine_execute.go:12')).toEqual({
      raw: 'pkg/actor/agent/turn_engine_execute.go:12',
      path: 'pkg/actor/agent/turn_engine_execute.go',
      ext: 'go',
      line: 12,
    })
  })

  it('parses basename without line number', () => {
    expect(parseFileReference('agent.go')).toEqual({
      raw: 'agent.go',
      path: 'agent.go',
      ext: 'go',
      line: undefined,
    })
  })

  it('normalizes windows separators', () => {
    expect(parseFileReference('pkg\\actor\\agent.go:5')).toEqual({
      raw: 'pkg\\actor\\agent.go:5',
      path: 'pkg/actor/agent.go',
      ext: 'go',
      line: 5,
    })
  })

  it('strips surrounding punctuation', () => {
    expect(parseFileReference('`agent.go:10`')).toEqual({
      raw: 'agent.go:10',
      path: 'agent.go',
      ext: 'go',
      line: 10,
    })
  })

  it('parses text and config file references', () => {
    expect(parseFileReference('notes.txt:2')).toEqual({
      raw: 'notes.txt:2',
      path: 'notes.txt',
      ext: 'txt',
      line: 2,
    })
    expect(parseFileReference('config.ini:10')).toEqual({
      raw: 'config.ini:10',
      path: 'config.ini',
      ext: 'ini',
      line: 10,
    })
  })

  it('parses markdown file references', () => {
    expect(parseFileReference('README.md:5')).toEqual({
      raw: 'README.md:5',
      path: 'README.md',
      ext: 'md',
      line: 5,
    })
  })

  it('parses dotfile references', () => {
    expect(parseFileReference('.gitignore')).toEqual({
      raw: '.gitignore',
      path: '.gitignore',
      ext: '.gitignore',
      line: undefined,
    })
    expect(parseFileReference('.editorconfig:12')).toEqual({
      raw: '.editorconfig:12',
      path: '.editorconfig',
      ext: '.editorconfig',
      line: 12,
    })
    expect(parseFileReference('path/to/.gitignore')).toEqual({
      raw: 'path/to/.gitignore',
      path: 'path/to/.gitignore',
      ext: '.gitignore',
      line: undefined,
    })
    expect(parseFileReference('.env.local:3')).toEqual({
      raw: '.env.local:3',
      path: '.env.local',
      ext: '.env.local',
      line: 3,
    })
  })

  it('rejects unknown dotfiles', () => {
    expect(parseFileReference('.foobar')).toBeNull()
    expect(parseFileReference('path/to/.secret')).toBeNull()
  })

  it('rejects URLs and arbitrary text', () => {
    expect(parseFileReference('https://example.com/foo.go:5')).toBeNull()
    expect(parseFileReference('check file.go and then bar.js')).toBeNull()
  })
})

describe('rehypeFileReferences', () => {
  function makeInlineCodeTree(text: string, parentTag = 'p'): Root {
    return {
      type: 'root',
      children: [{
        type: 'element',
        tagName: parentTag,
        properties: {},
        children: [{
          type: 'element',
          tagName: 'code',
          properties: {},
          children: [{ type: 'text', value: text }],
        }],
      }],
    }
  }

  it('wraps file reference inline code in anchor', () => {
    const tree = makeInlineCodeTree('turn_engine_execute.go:549')
    rehypeFileReferences({ projectId: 'p1', projectRoot: '/project' })(tree)

    const p = tree.children[0] as Element
    const a = p.children[0] as Element
    expect(a.tagName).toBe('a')
    expect(a.properties?.className).toEqual(['ai-file-ref'])
    expect(a.properties?.href).toBe('#')
    expect(a.properties?.['data-ai-file-path']).toBe('turn_engine_execute.go')
    expect(a.properties?.['data-ai-file-line']).toBe('549')
    expect(a.properties?.['data-ai-project-id']).toBe('p1')

    const code = a.children[0] as Element
    expect(code.tagName).toBe('code')
    expect(code.children[0]).toEqual({ type: 'text', value: 'turn_engine_execute.go:549' })
  })

  it('emits line-end data attribute for range reference', () => {
    const tree = makeInlineCodeTree('turn_engine_execute.go:549-560')
    rehypeFileReferences({ projectId: 'p1', projectRoot: '/project' })(tree)

    const p = tree.children[0] as Element
    const a = p.children[0] as Element
    expect(a.tagName).toBe('a')
    expect(a.properties?.['data-ai-file-line']).toBe('549')
    expect(a.properties?.['data-ai-file-line-end']).toBe('560')
  })

  it('skips non-file-reference inline code', () => {
    const tree = makeInlineCodeTree('some plain text')
    rehypeFileReferences({ projectId: 'p1', projectRoot: '/project' })(tree)

    const p = tree.children[0] as Element
    const code = p.children[0] as Element
    expect(code.tagName).toBe('code')
  })

  it('skips code blocks', () => {
    const tree = makeInlineCodeTree('main.go:10', 'pre')
    rehypeFileReferences({ projectId: 'p1', projectRoot: '/project' })(tree)

    const pre = tree.children[0] as Element
    const code = pre.children[0] as Element
    expect(code.tagName).toBe('code')
  })

  it('does nothing without project binding', () => {
    const tree = makeInlineCodeTree('main.go:10')
    rehypeFileReferences({ projectId: null, projectRoot: null })(tree)

    const p = tree.children[0] as Element
    const code = p.children[0] as Element
    expect(code.tagName).toBe('code')
  })
})
