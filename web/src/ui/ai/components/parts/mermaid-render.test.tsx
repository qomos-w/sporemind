import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { MermaidRender, clearMermaidCanvasHeightCache } from './mermaid-render'

// Mock dynamic import of mermaid
const mockRender = vi.fn()
const mockInitialize = vi.fn()

vi.mock('mermaid', () => ({
  default: {
    initialize: mockInitialize,
    render: mockRender,
  },
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('MermaidRender', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    clearMermaidCanvasHeightCache()
    // Default data-theme
    document.documentElement.setAttribute('data-theme', 'light')
    // Default mock returns a valid SVG
    mockRender.mockResolvedValue({ svg: '<svg viewBox="0 0 100 100"><rect width="100" height="100" fill="red"/></svg>' })
  })

  afterEach(async () => {
    document.documentElement.removeAttribute('data-theme')
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderMermaid = async (code: string) => {
    await act(async () => {
      root.render(<MermaidRender code={code} />)
    })
  }

  it('renders flowchart diagram', async () => {
    await renderMermaid('graph TD\nA-->B')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockInitialize).toHaveBeenCalled()
    expect(mockRender).toHaveBeenCalledWith(expect.any(String), 'graph TD\nA-->B')
  })

  it('renders flowchart with flowchart keyword', async () => {
    await renderMermaid('flowchart LR\nA-->B')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockRender).toHaveBeenCalled()
  })

  it('renders sequence diagram', async () => {
    await renderMermaid('sequenceDiagram\nA->>B: Hello')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockRender).toHaveBeenCalled()
  })

  it('renders class diagram', async () => {
    await renderMermaid('classDiagram\nclass Animal')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockRender).toHaveBeenCalled()
  })

  it('renders state diagram', async () => {
    await renderMermaid('stateDiagram\n[*] --> Idle')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockRender).toHaveBeenCalled()
  })

  it('renders ER diagram', async () => {
    await renderMermaid('erDiagram\nCUSTOMER ||--o{ ORDER : places')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockRender).toHaveBeenCalled()
  })

  it('shows fallback for unsupported diagram types', async () => {
    await renderMermaid('pie title Pets\nDog: 5\nCat: 3')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-fallback')).not.toBeNull()
    })
    // Should not call mermaid for unsupported types
    expect(mockRender).not.toHaveBeenCalled()
  })

  it('shows fallback for plain text', async () => {
    await renderMermaid('just some text')
    const fallback = container.querySelector('.ai-mermaid-fallback')
    expect(fallback).not.toBeNull()
    expect(fallback!.textContent).toBe('just some text')
    expect(mockRender).not.toHaveBeenCalled()
  })

  it('renders a recenter button that resets the view without error', async () => {
    await renderMermaid('graph TD\nA-->B')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    const recenter = container.querySelector('[title="Recenter"]') as HTMLButtonElement
    expect(recenter).not.toBeNull()
    await act(async () => { recenter.click() })
  })

  it('anchors a fitting diagram CENTER-x/TOP-y, and a wide one LEFT/TOP', async () => {
    // Fitting case: 200x400 image at z=13/16 (jsdom font fallback) is 162.5px
    // wide in a 300px canvas → centered; 325px tall in a 150px canvas (the
    // max-height-clamp case) → flush with the top, never a middle band.
    mockRender.mockResolvedValueOnce({
      svg: '<svg viewBox="0 0 200 400"><rect width="200" height="400" fill="red"/></svg>',
    })
    await renderMermaid('graph TD\nA-->B')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    // jsdom has no layout: give the canvas a fixed viewport box so the anchor
    // math runs for real.
    const canvas = container.querySelector('.ai-mermaid-canvas') as HTMLElement
    Object.defineProperty(canvas, 'clientWidth', { configurable: true, get: () => 300 })
    Object.defineProperty(canvas, 'clientHeight', { configurable: true, get: () => 150 })
    const recenter = container.querySelector('[title="Recenter"]') as HTMLButtonElement
    await act(async () => { recenter.click() })
    const stage = container.querySelector('.ai-mermaid-stage') as HTMLElement
    expect(stage.style.transform).toBe('translate(68.75px, 0px) scale(0.8125)')

    // Wide case: a 500-wide image (406.25px scaled) overflows the 300px
    // canvas → left-aligned so the opening columns lead the view.
    mockRender.mockResolvedValueOnce({
      svg: '<svg viewBox="0 0 500 100"><rect width="500" height="100" fill="red"/></svg>',
    })
    await act(async () => { root.render(<MermaidRender code={'flowchart LR\nA-->B'} />) })
    await vi.waitFor(() => {
      expect(stage.style.transform).not.toBe('translate(68.75px, 0px) scale(0.8125)')
    })
    await act(async () => { recenter.click() })
    expect(stage.style.transform).toBe('translate(0px, 0px) scale(0.8125)')
  })

  it('reserves the last canvas height on remount to avoid a scroll jump', async () => {
    // A JS string literal so both mounts pass the identical `code` (a JSX
    // attribute string would not interpret the \n escape).
    const code = 'graph TD\nA-->B'
    // First mount renders and caches the canvas height for this code.
    await renderMermaid(code)
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    const settled = (container.querySelector('.ai-mermaid-canvas') as HTMLElement).style.height
    expect(settled).toMatch(/^\d+px$/)

    // Simulate virtualization: tear the slot down, then remount on a fresh root.
    await act(async () => { root.unmount() })
    container.remove()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    // Stall the async mermaid.render so we can observe the pre-render state.
    let resolveRender!: (v: { svg: string }) => void
    mockRender.mockReturnValueOnce(new Promise<{ svg: string }>((r) => { resolveRender = r }))
    await act(async () => { root.render(<MermaidRender code={code} />) })

    // Even with no svg yet, the canvas already holds the reserved height — the
    // remounted message does not briefly shrink to 0 and snap back.
    const canvas = container.querySelector('.ai-mermaid-canvas') as HTMLElement
    expect(canvas).not.toBeNull()
    expect(canvas.style.height).toBe(settled)
    expect(container.querySelector('svg')).toBeNull()

    // Let the render resolve; the height stays consistent (no second snap).
    await act(async () => {
      resolveRender({ svg: '<svg viewBox="0 0 100 100"><rect width="100" height="100" fill="red"/></svg>' })
    })
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect((container.querySelector('.ai-mermaid-canvas') as HTMLElement).style.height).toBe(settled)
  })

  it('shows error message when rendering fails', async () => {
    mockRender.mockRejectedValue(new Error('Parse error at line 1'))
    await renderMermaid('graph TD\nBAD SYNTAX')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-error-msg')).not.toBeNull()
    })
    const errorMsg = container.querySelector('.ai-mermaid-error-msg')
    expect(errorMsg!.textContent).toContain('Parse error')
    // Fallback should also be shown
    expect(container.querySelector('.ai-mermaid-fallback')).not.toBeNull()
  })

  it('shows the COMPLETE code in the error fallback', async () => {
    mockRender.mockRejectedValue(new Error('Parse error on line 25'))
    const code = [
      'graph TD',
      ...Array.from({ length: 30 }, (_, i) => `N${i}["至少显示 3000ms · 上限 20s ${i}"] --> N${i + 1}`),
    ].join('\n')
    await renderMermaid(code)
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-error-msg')).not.toBeNull()
    })
    const fallback = container.querySelector('.ai-mermaid-fallback')
    expect(fallback).not.toBeNull()
    expect(fallback!.textContent).toBe(code)
  })

  it('handles theme change via data-theme attribute', async () => {
    await renderMermaid('graph TD\nA-->B')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    mockInitialize.mockClear()
    mockRender.mockClear()

    // Change theme
    await act(async () => {
      document.documentElement.setAttribute('data-theme', 'dark')
    })

    // Should re-render with dark theme
    await vi.waitFor(() => {
      expect(mockInitialize).toHaveBeenCalledWith(
        expect.objectContaining({ theme: 'dark' })
      )
    })
  })

  it('cleans up on unmount (no console errors)', async () => {
    await renderMermaid('graph TD\nA-->B')
    // Let render start but verify unmount doesn't throw
    await act(async () => {
      root.unmount()
    })
    // Re-create for afterEach cleanup
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  it('uses unique IDs for consecutive renders', async () => {
    vi.useFakeTimers()
    try {
      await act(async () => {
        root.render(<MermaidRender code="graph TD\nA-->B" />)
      })
      expect(mockRender).toHaveBeenCalledTimes(1)
      const firstId = mockRender.mock.calls[0]![0]
      mockRender.mockClear()

      // Different code re-renders (after the debounce) with a fresh ID.
      act(() => {
        root.render(<MermaidRender code="graph TD\nC-->D" />)
      })
      await act(async () => {
        vi.advanceTimersByTime(400)
      })
      expect(mockRender).toHaveBeenCalledTimes(1)
      const secondId = mockRender.mock.calls[0]![0]
      expect(firstId).not.toBe(secondId)
    } finally {
      vi.useRealTimers()
    }
  })

  it('initializes mermaid with startOnLoad: false and light theme', async () => {
    await renderMermaid('graph TD\nA-->B')
    await vi.waitFor(() => {
      expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
    })
    expect(mockInitialize).toHaveBeenCalledWith({
      startOnLoad: false,
      theme: 'default',
      securityLevel: 'loose',
      suppressErrorRendering: true,
      themeVariables: { fontSize: '16px' },
    })
  })

  it('debounces re-renders while code keeps changing (streaming partial fence)', async () => {
    vi.useFakeTimers()
    try {
      await act(async () => {
        root.render(<MermaidRender code="graph TD\nA-->B" />)
      })
      // First render for a mount is immediate (act flushes the import promise).
      expect(mockRender).toHaveBeenCalledTimes(1)

      // Streaming partial fences: each change resets the debounce timer, so
      // mermaid.render is not re-invoked per tick.
      const partials = ['graph TD\nA-->B\n', 'graph TD\nA-->B\nB--', 'graph TD\nA-->B\nB-->C']
      for (const p of partials) {
        act(() => {
          root.render(<MermaidRender code={p} />)
        })
        await act(async () => {
          vi.advanceTimersByTime(100)
        })
      }
      expect(mockRender).toHaveBeenCalledTimes(1)

      // Code settled: after the debounce window the final code renders once.
      await act(async () => {
        vi.advanceTimersByTime(300)
      })
      expect(mockRender).toHaveBeenCalledTimes(2)
      expect(mockRender).toHaveBeenLastCalledWith(expect.any(String), 'graph TD\nA-->B\nB-->C')
    } finally {
      vi.useRealTimers()
    }
  })

  describe('expand overlay', () => {
    const expandButton = () => container.querySelector('[title="Expand"]') as HTMLButtonElement | null

    it('toolbar offers an expand button next to the recenter button', async () => {
      await renderMermaid('graph TD\nA-->B')
      await vi.waitFor(() => {
        expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
      })
      expect(expandButton()).not.toBeNull()
      expect(document.querySelector('.ai-mermaid-expand-root')).toBeNull()
    })

    it('clicking expand opens a floating overlay canvas rendering the same diagram', async () => {
      await renderMermaid('graph TD\nA-->B')
      await vi.waitFor(() => {
        expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
      })
      await act(async () => { expandButton()!.click() })
      const overlay = document.querySelector('.ai-mermaid-expand-root') as HTMLElement
      expect(overlay).not.toBeNull()
      expect(overlay.querySelector('.ai-mermaid-expand-backdrop')).not.toBeNull()
      // Overlay hosts its own canvas of the same code (same display scale).
      await vi.waitFor(() => {
        expect(overlay.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
      })
      expect(mockRender).toHaveBeenCalledWith(expect.any(String), 'graph TD\nA-->B')
    })

    it('closes via the toolbar Close button, the backdrop, or Escape', async () => {
      await renderMermaid('graph TD\nA-->B')
      await vi.waitFor(() => {
        expect(container.querySelector('.ai-mermaid-canvas svg')).not.toBeNull()
      })

      const openOverlay = async () => {
        await act(async () => { expandButton()!.click() })
        expect(document.querySelector('.ai-mermaid-expand-root')).not.toBeNull()
      }

      await openOverlay()
      const closeBtn = document.querySelector('.ai-mermaid-expand-root [title="Close"]') as HTMLButtonElement
      await act(async () => { closeBtn.click() })
      expect(document.querySelector('.ai-mermaid-expand-root')).toBeNull()

      await openOverlay()
      const backdrop = document.querySelector('.ai-mermaid-expand-backdrop') as HTMLElement
      await act(async () => { backdrop.click() })
      expect(document.querySelector('.ai-mermaid-expand-root')).toBeNull()

      await openOverlay()
      await act(async () => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      })
      expect(document.querySelector('.ai-mermaid-expand-root')).toBeNull()
    })
  })
})
