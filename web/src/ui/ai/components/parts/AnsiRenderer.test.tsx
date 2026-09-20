import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AnsiText } from './AnsiRenderer'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('AnsiText', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderText = async (text: string) => {
    await act(async () => {
      root.render(<AnsiText text={text} />)
    })
  }

  it('marks spans with an ANSI background as ansi-bg', async () => {
    await renderText('\x1b[41mhighlighted\x1b[0m')
    const span = container.querySelector('span.ansi-bg')
    expect(span).not.toBeNull()
    expect(span!.textContent).toBe('highlighted')
    expect((span as HTMLElement).style.backgroundColor).toBe('var(--ansi-red)')
  })

  it('does not mark plain spans as ansi-bg', async () => {
    await renderText('plain text')
    const span = container.querySelector('span')
    expect(span).not.toBeNull()
    expect(span!.classList.contains('ansi-bg')).toBe(false)
  })

  it('keeps explicit foreground color on a background span', async () => {
    await renderText('\x1b[37;41mwhite on red\x1b[0m')
    const span = container.querySelector('span.ansi-bg')
    expect(span).not.toBeNull()
    expect((span as HTMLElement).style.color).toBe('var(--ansi-white)')
    expect((span as HTMLElement).style.backgroundColor).toBe('var(--ansi-red)')
  })

  describe('incremental parsing', () => {
    const renderHtmlInFreshRoot = async (text: string): Promise<string> => {
      const c = document.createElement('div')
      document.body.appendChild(c)
      const r = createRoot(c)
      await act(async () => {
        r.render(<AnsiText text={text} />)
      })
      const html = c.innerHTML
      await act(async () => {
        r.unmount()
      })
      c.remove()
      return html
    }

    const renderChunked = async (chunks: string[]): Promise<string> => {
      let acc = ''
      for (const chunk of chunks) {
        acc += chunk
        // eslint-disable-next-line no-await-in-loop
        await act(async () => {
          root.render(<AnsiText text={acc} />)
        })
      }
      return container.innerHTML
    }

    const chunkEvery = (text: string, size: number): string[] => {
      const chunks: string[] = []
      for (let i = 0; i < text.length; i += size) chunks.push(text.slice(i, i + size))
      return chunks
    }

    it('chunked append renders identically to one-shot parse', async () => {
      const full = 'plain \x1b[1;31mbold red\x1b[0m tail \x1b[42m bg \x1b[0m end'
      const chunked = await renderChunked(chunkEvery(full, 3))
      const oneShot = await renderHtmlInFreshRoot(full)
      expect(chunked).toBe(oneShot)
    })

    it('waits for the next chunk when an ESC sequence is split mid-way', async () => {
      const chunks = ['start \x1b[3', '1;42mcolored\x1b[', '0m done']
      let acc = ''
      for (const chunk of chunks.slice(0, 1)) {
        acc += chunk
        // eslint-disable-next-line no-await-in-loop
        await act(async () => {
          root.render(<AnsiText text={acc} />)
        })
      }
      // The incomplete "\x1b[3" must not leak a truncated sequence or state:
      // only the plain text before it renders.
      expect(container.textContent).toBe('start ')
      const chunked = await renderChunked(chunks)
      const oneShot = await renderHtmlInFreshRoot('start \x1b[31;42mcolored\x1b[0m done')
      expect(chunked).toBe(oneShot)
    })

    it('full reparse on non-append text swap of equal length', async () => {
      const first = 'a'.repeat(17)
      const second = '\x1b[31m' + 'b'.repeat(7) + '\x1b[0m!'
      expect(second.length).toBe(first.length)
      await renderText(first)
      expect(container.textContent).toBe(first)
      await renderText(second)
      const span = container.querySelector('span')
      expect(span).not.toBeNull()
      expect(span!.textContent).toBe('b'.repeat(7))
      expect((span as HTMLElement).style.color).toBe('var(--ansi-red)')
    })
  })
})
