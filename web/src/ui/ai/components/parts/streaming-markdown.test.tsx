import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { useStreamingMarkdownChunks, isOpenDiagramSource } from './streaming-markdown'
import { splitMarkdownChunks, splitMarkdownChunksWithSpans, type MarkdownChunk } from './markdown-chunks'
import { processWikiWords } from './wikiword'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

/** Reference implementation: the full non-incremental pipeline. The splitter
 *  emits empty trailing chunks on blank-line boundaries that the incremental
 *  cache filters out (they render as empty spans anyway); normalize both
 *  sides identically so the comparison is on the visible structure. */
function fullPipeline(content: string): MarkdownChunk[] {
  return splitMarkdownChunks(processWikiWords(content)).filter(c => c.content !== '')
}

const doc = [
  'First paragraph mentions ProjectSummary and [[Card Link]].',
  '',
  'Second paragraph with `InlineCode` reference.',
  '',
  '```go',
  'func main() {',
  '\tfmt.Println("hello")',
  '}',
  '```',
  '',
  '- list item one',
  '- list item two',
  '',
  'Final growing paragraph tail',
].join('\n')

describe('splitMarkdownChunksWithSpans', () => {
  it('matches splitMarkdownChunks content and finalized flags', () => {
    const spans = splitMarkdownChunksWithSpans(doc)
    const plain = splitMarkdownChunks(doc)
    expect(spans.map(({ content, finalized }) => ({ content, finalized }))).toEqual(plain)
  })

  it('spans are ordered, clamped to text length, and cover the text', () => {
    for (const text of [doc, doc + '\n', 'single line', '```\nunclosed', '']) {
      const spans = splitMarkdownChunksWithSpans(text)
      let prevEnd = 0
      for (const s of spans) {
        expect(s.rawStart).toBe(prevEnd)
        expect(s.rawEnd).toBeGreaterThan(s.rawStart)
        expect(s.rawEnd).toBeLessThanOrEqual(text.length)
        prevEnd = s.rawEnd
      }
    }
  })

  it('span slice reproduces the chunk content up to trimming', () => {
    const spans = splitMarkdownChunksWithSpans(doc)
    for (const s of spans) {
      const raw = doc.slice(s.rawStart, s.rawEnd)
      // Finalized chunks trim trailing whitespace; streaming chunks carry the
      // splitter's synthesized trailing newline.
      expect(s.content).toBe(s.finalized ? raw.trimEnd() : raw.trimStart() + '\n')
    }
  })
})

describe('useStreamingMarkdownChunks', () => {
  let container: HTMLDivElement
  let root: Root
  let latest: MarkdownChunk[] | null | undefined

  function render(content: string, growing = true) {
    act(() => {
      root.render(<Harness content={content} growing={growing} />)
    })
  }

  function Harness({ content, growing }: { content: string; growing: boolean }) {
    latest = useStreamingMarkdownChunks(content, growing)
    return null
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    latest = undefined
  })

  it('returns null when the content is settled (not growing)', () => {
    render(doc, false)
    expect(latest).toBeNull()
    render('short', false)
    expect(latest).toBeNull()
  })

  it('chunks growing content at any length — no short-content full-markdown fallback', () => {
    // Regression: a growing message under the old 200-char threshold used to
    // fall back to full <Markdown> re-parsing, feeding a partial mermaid
    // fence to the diagram renderer on every reveal tick.
    render('```merm', true)
    expect(latest).not.toBeNull()
    render('```mermaid\nflowchart TD\n  A --> B', true)
    expect(latest).not.toBeNull()
    const tail = latest![latest!.length - 1]!
    expect(tail.finalized).toBe(false)
    expect(tail.content).toContain('flowchart TD')
  })

  it('finalizes a mermaid fence only once the closing fence arrives', () => {
    const open = '```mermaid\nflowchart TD\n  A --> B\n'
    render(open, true)
    expect(latest!.every(c => !c.finalized)).toBe(true)

    const closed = open + '```'
    render(closed, true)
    const fence = latest!.find(c => c.content.startsWith('```mermaid'))
    expect(fence).toBeDefined()
    expect(fence!.finalized).toBe(true)
    expect(fence!.content.endsWith('```')).toBe(true)
  })

  it('matches the full pipeline at every growth step', () => {
    for (let len = 0; len <= doc.length; len += 7) {
      render(doc.slice(0, len))
      expect(latest).toEqual(fullPipeline(doc.slice(0, len)))
    }
    render(doc)
    expect(latest).toEqual(fullPipeline(doc))
  })

  it('reuses cached chunk objects for finalized blocks', () => {
    render(doc.slice(0, 400))
    const mid = latest!
    render(doc)
    const full = latest!
    const sameObjects = full.filter((_c, i) => mid[i] !== undefined && full[i] === mid[i]).length
    expect(sameObjects).toBeGreaterThan(0)
  })

  it('resets the cache when content is replaced (non-append)', () => {
    render(doc.slice(0, 300))
    const replacement = 'Completely different text that is long enough on its own to pass the streaming threshold so the incremental pipeline is exercised rather than the short-circuit null path, which requires more than two hundred characters of prose to trigger.'
    render(replacement)
    expect(latest).toEqual(fullPipeline(replacement))
  })
})

describe('isOpenDiagramSource', () => {
  it('matches a tail starting with an open mermaid fence', () => {
    expect(isOpenDiagramSource('```mermaid')).toBe(true)
    expect(isOpenDiagramSource('```mermaid\nflowchart TD\n  A --> B')).toBe(true)
    expect(isOpenDiagramSource('  ```mermaid\nflowchart TD')).toBe(true)
    expect(isOpenDiagramSource('```MERMAID\nflowchart TD')).toBe(true)
  })

  it('rejects other fences, prose, and other languages', () => {
    expect(isOpenDiagramSource('```go\nfunc main() {}')).toBe(false)
    expect(isOpenDiagramSource('```mermaidchart\nflowchart TD')).toBe(false)
    expect(isOpenDiagramSource('```wireframe\nWireframe mobile')).toBe(false)
    expect(isOpenDiagramSource('Some prose before anything else')).toBe(false)
    expect(isOpenDiagramSource('')).toBe(false)
  })
})
