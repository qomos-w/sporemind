import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { TextBlock, smartSlice } from './TextBlock'
import { SmoothStreamContext } from '../../context/SmoothStreamContext'
import type { TextFrame } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

// Stable identity: TextBlock memoizes wikiComponents on `t`; an unstable `t`
// here would cascade into MemoizedMarkdown re-parsing and code-subtree
// remounts that never happen in production.
const stableT = vi.hoisted(() => (key: string) => key)

vi.mock('../../../../i18n/provider', () => ({
  useI18n: () => ({ t: stableT }),
}))

const { mockInitialize, mockRender } = vi.hoisted(() => ({
  mockInitialize: vi.fn(),
  mockRender: vi.fn(),
}))

vi.mock('../../voice-synthesis', () => ({
  speak: vi.fn(),
  stop: vi.fn(),
  isSpeaking: () => false,
}))

vi.mock('mermaid', () => ({
  default: {
    initialize: mockInitialize,
    render: mockRender,
  },
}))

const MERMAID_SVG = '<svg viewBox="0 0 100 100"><circle r="10"/></svg>'

function makeFrame(status: 'running' | 'completed', content: string): TextFrame {
  return { id: 'f1', type: 'text', status, content }
}

describe('TextBlock mermaid streaming', () => {
  let container: HTMLDivElement
  let root: Root

  const renderFrame = async (frame: TextFrame) => {
    await act(async () => {
      root.render(
        // smoothStream off → reveal is synchronous, so growing === isStreaming
        <SmoothStreamContext.Provider value={{ smoothStream: false, isStreaming: frame.status === 'running' } as any}>
          <TextBlock frame={frame} />
        </SmoothStreamContext.Provider>,
      )
    })
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mockInitialize.mockReset()
    mockRender.mockReset()
    mockRender.mockResolvedValue({ svg: MERMAID_SVG })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('renders an unclosed mermaid fence as a fixed placeholder, not raw source', async () => {
    await renderFrame(makeFrame('running', '```mermaid\nflowchart TD\n  A --> B\n  B --> C'))
    const card = container.querySelector('.ai-diagram-streaming')
    expect(card).not.toBeNull()
    // Raw source must NOT be laid out (it would grow the stream per tick).
    expect(container.querySelector('.streaming-tail')).toBeNull()
    expect(container.textContent).not.toContain('flowchart TD')
    // Line count reflects progress.
    expect(card!.querySelector('.ai-diagram-streaming-count')!.textContent).toBe('3')
    // No diagram renderer mounted while the fence is open.
    expect(mockRender).not.toHaveBeenCalled()
  })

  it('mounts the diagram once the closing fence arrives mid-stream', async () => {
    const content = 'Intro prose.\n\n```mermaid\nflowchart TD\n  A --> B\n```\n\nOutro prose.'
    await renderFrame(makeFrame('running', content))
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockRender).toHaveBeenCalledTimes(1)
    expect(mockRender).toHaveBeenCalledWith(expect.any(String), 'flowchart TD\n  A --> B\n')
  })

  it('does not remount the diagram when the frame settles (chunk freeze)', async () => {
    const content = 'Intro prose.\n\n```mermaid\nflowchart TD\n  A --> B\n```\n\nOutro prose.'
    await renderFrame(makeFrame('running', content))
    await vi.waitFor(() => {
      expect(mockRender).toHaveBeenCalledTimes(1)
    })

    // Stream ends, reveal drained: content unchanged, status flips completed.
    await renderFrame(makeFrame('completed', content))
    // Re-render settled frame once more for good measure.
    await renderFrame(makeFrame('completed', content))

    expect(mockRender).toHaveBeenCalledTimes(1)
    expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    // The frozen chunked tree keeps plain chunks as markdown, not raw tails.
    expect(container.querySelector('.streaming-tail')).toBeNull()
  })

  it('renders the full markdown pass for a settled frame that never grew', async () => {
    await renderFrame(makeFrame('completed', '```mermaid\nflowchart TD\n  A --> B\n```'))
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockRender).toHaveBeenCalledTimes(1)
  })

  it('does not remount diagrams on unrelated re-renders of settled history', async () => {
    // History-loaded frames take the non-chunked path; a conversation-level
    // re-render (another message streaming, composer state, …) re-renders
    // TextBlock with a fresh frame object but identical content. The diagram
    // must not remount — its immediate first render is a full redraw.
    const content = '```mermaid\nflowchart TD\n  A --> B\n```'
    await renderFrame(makeFrame('completed', content))
    await vi.waitFor(() => {
      expect(mockRender).toHaveBeenCalledTimes(1)
    })

    for (let i = 0; i < 5; i++) {
      await renderFrame(makeFrame('completed', content))
    }

    expect(mockRender).toHaveBeenCalledTimes(1)
    expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
  })
})

describe('smartSlice', () => {
  it('returns short content unchanged', () => {
    expect(smartSlice('hello', 10)).toBe('hello')
  })

  it('cuts at max when no fence is present', () => {
    expect(smartSlice('abcdefghij', 4)).toBe('abcd')
  })

  it('cuts at max when all fences up to max are closed', () => {
    const s = 'aa```x```zzzz'
    // max lands after the closed fence (index 2-8), so cut at max
    expect(smartSlice(s, 9)).toBe(s.slice(0, 9))
  })

  it('includes a fence that closes shortly after max', () => {
    // fence opens at 3 (```), closer at 20; max=10 within grace of closer
    const s = 'aaa```mermaid\nAAA\n```tail'
    const out = smartSlice(s, 10)
    expect(out.endsWith('```')).toBe(true)
    expect(out).toBe(s.slice(0, s.indexOf('```', 6) + 3))
  })

  it('cuts before an unclosed opener whose fence ends far beyond max', () => {
    const body = 'x'.repeat(5000)
    const s = 'intro\n```code\n' + body + '\n```'
    const max = 100 // opener at 6, closer far beyond max+grace
    const out = smartSlice(s, max)
    expect(out).toBe('intro')
    expect(out.includes('```')).toBe(false)
  })
})
