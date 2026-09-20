import { describe, it, expect } from 'vitest'
import { processWikiWords, extractWikiWordTargets } from './wikiword'

describe('processWikiWords', () => {
  describe('[[...]] explicit links', () => {
    it('converts [[CardId]] to markdown link', () => {
      const result = processWikiWords('see [[ProjectSummary]] for details')
      expect(result).toBe('see [ProjectSummary](wiki:ProjectSummary) for details')
    })

    it('converts [[display|target]] to markdown link with custom text', () => {
      const result = processWikiWords('see [[Custom Text|target-card]] here')
      expect(result).toBe('see [Custom Text](wiki:target-card) here')
    })

    it('escapes markdown emphasis chars in link text', () => {
      const result = processWikiWords('card is [[__builtin_summary__]]')
      // __ must be escaped to prevent CommonMark bold parsing
      expect(result).toBe('card is [\\_\\_builtin\\_summary\\_\\_](wiki:__builtin_summary__)')
    })

    it('preserves original cardId in wiki: URL even when text is escaped', () => {
      const result = processWikiWords('[[__builtin_constraints__]]')
      expect(result).toContain('](wiki:__builtin_constraints__)')
    })
  })

  describe('markdown-style [display](wiki:target) links', () => {
    it('preserves [display](wiki:target) as-is', () => {
      const result = processWikiWords('see [Custom Text](wiki:target-card) here')
      expect(result).toBe('see [Custom Text](wiki:target-card) here')
    })

    it('does not convert CamelCase inside existing wiki markdown links', () => {
      const result = processWikiWords('[ProjectSummary](wiki:ProjectSummary)')
      expect(result).toBe('[ProjectSummary](wiki:ProjectSummary)')
    })

    it('extracts target from [display](wiki:target)', () => {
      const result = extractWikiWordTargets('see [Custom Text](wiki:target-card) here')
      expect(result).toContain('target-card')
    })

    it('extracts both bracket and markdown-style wiki link targets', () => {
      const result = extractWikiWordTargets('[[CardId]] and [Display](wiki:OtherCard)')
      expect(result).toEqual(expect.arrayContaining(['CardId', 'OtherCard']))
    })
  })

  describe('CamelCase auto-detection in prose', () => {
    it('converts CamelCase words to markdown links', () => {
      const result = processWikiWords('see ProjectSummary for details')
      expect(result).toBe('see [ProjectSummary](wiki:ProjectSummary) for details')
    })

    it('detects multiple CamelCase words on one line', () => {
      const result = processWikiWords('ProjectSummary and ApiKeyManager')
      expect(result).toBe('[ProjectSummary](wiki:ProjectSummary) and [ApiKeyManager](wiki:ApiKeyManager)')
    })

    it('does not detect CamelCase inside code blocks', () => {
      const result = processWikiWords('```\nProjectSummary\n```')
      expect(result).toBe('```\nProjectSummary\n```')
    })

    it('does not detect CamelCase inside existing markdown links', () => {
      const result = processWikiWords('[ProjectSummary](https://example.com)')
      expect(result).toBe('[ProjectSummary](https://example.com)')
    })
  })

  describe('backtick CamelCase', () => {
    it('converts `CamelCase` to wiki link with code styling', () => {
      const result = processWikiWords('use `ProjectSummary` here')
      expect(result).toBe('use [`ProjectSummary`](wiki:ProjectSummary) here')
    })

    it('leaves non-CamelCase inline code untouched', () => {
      const result = processWikiWords('use `some_variable` here')
      expect(result).toBe('use `some_variable` here')
    })
  })

  describe('backtick file references — not wikiwords', () => {
    it('leaves `file.go` untouched for file-reference plugin', () => {
      const result = processWikiWords('see `file.go` for details')
      expect(result).toBe('see `file.go` for details')
    })

    it('leaves `path/to/file.ts` untouched', () => {
      const result = processWikiWords('edit `src/main.ts:42`')
      expect(result).toBe('edit `src/main.ts:42`')
    })

    it('leaves `Component.tsx` untouched even though it contains CamelCase', () => {
      const result = processWikiWords('modify `MonoCard.tsx`')
      expect(result).toBe('modify `MonoCard.tsx`')
    })

    it('leaves `.gitignore` untouched', () => {
      const result = processWikiWords('check `.gitignore`')
      expect(result).toBe('check `.gitignore`')
    })
  })

  describe('code block protection', () => {
    it('leaves fenced code blocks untouched', () => {
      const input = '```\n[[CardId]] and ProjectSummary\n```'
      expect(processWikiWords(input)).toBe(input)
    })

    it('handles multi-line code blocks', () => {
      const input = '```\nline1\n[[CardId]]\nline3\n```'
      expect(processWikiWords(input)).toBe(input)
    })
  })

  describe('mixed syntax', () => {
    it('handles bracket, CamelCase, and backtick in one message', () => {
      const result = processWikiWords('[[CardId]] and ProjectSummary and `WikiWord`')
      expect(result).toContain('[CardId](wiki:CardId)')
      expect(result).toContain('[ProjectSummary](wiki:ProjectSummary)')
      expect(result).toContain('[`WikiWord`](wiki:WikiWord)')
    })

    it('handles wikiword and file reference on same line', () => {
      const result = processWikiWords('see `ProjectSummary` and `file.go`')
      expect(result).toContain('[`ProjectSummary`](wiki:ProjectSummary)')
      expect(result).toContain('`file.go`')
    })
  })

  describe('URL encoding of targets with spaces/special chars', () => {
    it('encodes spaces in [[...]] target', () => {
      const result = processWikiWords('[[Provider 冷却与配额耗尽重构设计]]')
      const url = result.match(/\]\(wiki:([^)]+)\)/)?.[1] || ''
      expect(url).toContain('%20')
      expect(url).not.toMatch(/ /) // no literal spaces in the URL
    })

    it('encodes spaces in [[display|target]] target', () => {
      const result = processWikiWords('[[显示|Some Card Title]]')
      expect(result).toContain('](wiki:Some%20Card%20Title)')
    })

    it('encodes spaces in [display](wiki:target) markdown links', () => {
      const result = processWikiWords('[Provider 冷却与配额耗尽重构设计](wiki:Provider 冷却与配额耗尽重构设计)')
      const url = result.match(/\]\(wiki:([^)]+)\)/)?.[1] || ''
      expect(url).toContain('%20')
      expect(url).not.toMatch(/ /)
    })

    it('round-trips: decodeURIComponent recovers original target from [[...]]', () => {
      const result = processWikiWords('[[Provider 冷却与配额耗尽重构设计]]')
      const href = result.match(/\]\(wiki:([^)]+)\)/)?.[1] || ''
      const decoded = decodeURIComponent(href)
      expect(decoded).toBe('Provider 冷却与配额耗尽重构设计')
    })

    it('round-trips: decodeURIComponent recovers original target from [display](wiki:target)', () => {
      const result = processWikiWords('[link](wiki:Provider 冷却与配额耗尽重构设计)')
      const href = result.match(/\]\(wiki:([^)]+)\)/)?.[1] || ''
      const decoded = decodeURIComponent(href)
      expect(decoded).toBe('Provider 冷却与配额耗尽重构设计')
    })

    it('does not double-encode [[...]] targets', () => {
      const result = processWikiWords('[[Some Card]]')
      expect(result).toContain('](wiki:Some%20Card)')
      expect(result).not.toContain('%25')
    })

    it('leaves simple alphanumeric targets unchanged', () => {
      const result = processWikiWords('[[ProjectSummary]]')
      expect(result).toBe('[ProjectSummary](wiki:ProjectSummary)')
    })
  })
})
