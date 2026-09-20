import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ToolCodeBlock } from './ToolViewPrimitives'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('ToolCodeBlock', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('renders non-empty text payload', async () => {
    await act(async () => { root.render(<ToolCodeBlock>{'hello\nworld'}</ToolCodeBlock>) })
    expect(container.querySelector('pre')).not.toBeNull()
    expect(container.textContent).toContain('hello')
  })

  it('does not render when the payload is only whitespace', async () => {
    await act(async () => { root.render(<ToolCodeBlock>{'  \n\t\r  '}</ToolCodeBlock>) })
    expect(container.querySelector('.ai-tool-code-wrapper')).toBeNull()
    expect(container.querySelector('pre')).toBeNull()
  })

  it('does not render running wrapper when the payload is only whitespace', async () => {
    await act(async () => { root.render(<ToolCodeBlock running>{' \n\t'}</ToolCodeBlock>) })
    expect(container.querySelector('.ai-tool-code-wrapper')).toBeNull()
  })

  it('does not render collapsed wrapper when the payload is only whitespace', async () => {
    await act(async () => { root.render(<ToolCodeBlock maxLines={2}>{' \n\t\n '}</ToolCodeBlock>) })
    expect(container.querySelector('.ai-tool-code-wrapper')).toBeNull()
    expect(container.querySelector('pre')).toBeNull()
  })

  it('renders mixed React children without suppression', async () => {
    await act(async () => { root.render(<ToolCodeBlock><span className="cursor">_</span></ToolCodeBlock>) })
    expect(container.querySelector('.ai-tool-code-wrapper')).toBeNull()
    expect(container.querySelector('.cursor')).not.toBeNull()
  })

  it('collapses non-whitespace multi-line content above maxLines', async () => {
    const text = Array.from({ length: 10 }, (_, i) => `line ${i}`).join('\n')
    await act(async () => { root.render(<ToolCodeBlock maxLines={5}>{text}</ToolCodeBlock>) })
    expect(container.querySelector('.ai-tool-code-wrapper')).not.toBeNull()
    expect(container.textContent).toContain('+5 more lines')
  })

  it('renders a running scroll tail without collapsing', async () => {
    const text = Array.from({ length: 10 }, (_, i) => `line ${i}`).join('\n')
    await act(async () => { root.render(<ToolCodeBlock maxLines={5} running>{text}</ToolCodeBlock>) })
    const pre = container.querySelector('pre.running-scroll')
    expect(pre).not.toBeNull()
    // Running tail shows the full content with no collapse expander.
    expect(container.querySelector('.ai-tool-code-expander')).toBeNull()
    expect(pre!.textContent).toContain('line 9')
  })

  it('accepts smoothScroll without crashing the running tail', async () => {
    const text = Array.from({ length: 10 }, (_, i) => `line ${i}`).join('\n')
    await act(async () => { root.render(<ToolCodeBlock maxLines={5} running smoothScroll>{text}</ToolCodeBlock>) })
    expect(container.querySelector('pre.running-scroll')).not.toBeNull()
  })
})
