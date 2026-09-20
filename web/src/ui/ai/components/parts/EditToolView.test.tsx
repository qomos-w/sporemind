import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { EditToolView } from './EditToolView'
import { SmoothStreamContext } from '../../context/SmoothStreamContext'
import { DEFAULT_UI_EXPANSION_MODES } from '../../../../application/workspace-ui-state'
import type { ToolFrame } from '../../model/frame-types'

const waitFrames = (n: number) => new Promise<void>(resolve => {
  const step = () => { if (n-- > 0) requestAnimationFrame(step); else resolve() }
  requestAnimationFrame(step)
})

const { onOpenFileMock } = vi.hoisted(() => ({ onOpenFileMock: vi.fn() }))

vi.mock('../../context/AIShellContext', () => ({
  useAIShellContext: () => ({ onOpenFile: onOpenFileMock }),
}))

vi.mock('./review-store', () => ({
  setReviewState: vi.fn(),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function makeFrame(status: ToolFrame['status'], input: object): ToolFrame {
  return {
    id: 'f1',
    type: 'tool',
    status,
    toolName: 'edit',
    input: JSON.stringify(input),
    version: 1,
  } as unknown as ToolFrame
}

const editInput = {
  Path: 'src/app.ts',
  Old_string: 'a',
  New_string: 'b',
}

describe('EditToolView direct display (smoothStream off)', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    onOpenFileMock.mockClear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('mounts a completed mid-stream frame open with no title-first delay', async () => {
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: false, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={makeFrame('completed', editInput)} />
        </SmoothStreamContext.Provider>
      )
    })
    const collapsible = container.querySelector('.ai-collapsible')!
    // Direct display: open at once, no choreography classes.
    expect(collapsible.classList.contains('open')).toBe(true)
    expect(collapsible.classList.contains('title-first')).toBe(false)
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()
  })

  it('mounts a running frame open directly and stays open after completion', async () => {
    const frame = makeFrame('running', editInput)
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: false, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    const collapsible = container.querySelector('.ai-collapsible')!
    expect(collapsible.classList.contains('open')).toBe(true)
    expect(collapsible.classList.contains('title-first')).toBe(false)
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()

    await act(async () => {
      frame.status = 'completed'
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: false, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    // fold-expand terminal state is open; completion keeps it open instantly.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
  })

  it('still allows manual collapse after completion', async () => {
    const frame = makeFrame('completed', editInput)
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: false, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    const header = container.querySelector('.ai-step-row') as HTMLElement
    await act(async () => {
      header.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(false)
  })
})

describe('EditToolView expansion', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    onOpenFileMock.mockClear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  it('renders at the configured terminal state when history-loaded (fold-expand -> open)', async () => {
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: false, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={makeFrame('completed', editInput)} />
        </SmoothStreamContext.Provider>
      )
    })
    // Terminal state of fold-expand is OPEN; history mounts render it
    // directly with no title delay / transition replay.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()
    // Title row is present and clickable for manual collapse.
    expect(container.querySelector('.ai-step-row')).toBeTruthy()
  })

  it('opens immediately with a CSS title-first transition when a completed frame mounts mid-stream', async () => {
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={makeFrame('completed', editInput)} />
        </SmoothStreamContext.Provider>
      )
    })
    await act(async () => { await waitFrames(4) })
    const collapsible = container.querySelector('.ai-collapsible')!
    expect(collapsible.classList.contains('open')).toBe(true)
    expect(collapsible.classList.contains('title-first')).toBe(true)
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()
  })

  it('unfolds via title delay even when running completes before the delay fires', async () => {
    const frame = makeFrame('running', editInput)
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    // The body mounts immediately; CSS owns the title-first visual hold.
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()

    // Tool finishes while the CSS transition is in progress.
    await act(async () => {
      frame.status = 'completed'
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
    expect(container.querySelector('.ai-collapsible')!.classList.contains('title-first')).toBe(true)
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()
  })

  it('mounts the body while running and keeps it open after completion', async () => {
    const frame = makeFrame('running', editInput)
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()
    expect(container.querySelector('.ai-collapsible')!.classList.contains('title-first')).toBe(true)

    await act(async () => {
      frame.status = 'completed'
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    // autoOpened latches: stays expanded after done, no auto-collapse.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
  })

  it('keeps the CSS title-first state when displaced by a later frame', async () => {
    const frame = makeFrame('completed', editInput)
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} turnStreaming={false} />
        </SmoothStreamContext.Provider>
      )
    })
    await act(async () => { await waitFrames(4) })
    const collapsible = container.querySelector('.ai-collapsible')!
    expect(collapsible.classList.contains('open')).toBe(true)
    expect(collapsible.classList.contains('title-first')).toBe(true)
    expect(container.querySelector('.ai-edit-card-diff')).toBeTruthy()
  })

  it('collapses manually via the header toggle after completion', async () => {
    const frame = makeFrame('completed', editInput)
    await act(async () => {
      root.render(<EditToolView frame={frame} />)
    })
    // No provider -> context default (fold-expand) with turnStreaming false:
    // history mount renders at terminal state, already open.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
    const header = container.querySelector('.ai-step-row') as HTMLElement
    await act(async () => {
      header.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(false)
  })

  it('opens the file in the right panel tab when the diff text area is clicked', async () => {
    const frame = makeFrame('completed', editInput)
    await act(async () => {
      root.render(<EditToolView frame={frame} />)
    })
    const diffArea = container.querySelector('.ai-edit-card-diff') as HTMLElement
    expect(diffArea.classList.contains('ai-edit-card-diff--openable')).toBe(true)
    await act(async () => {
      diffArea.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onOpenFileMock).toHaveBeenCalledTimes(1)
    expect(onOpenFileMock.mock.calls[0]![0]).toBe('src/app.ts')
    // The click must not toggle the fold.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
  })

  it('does not open the file when the diff expander button is clicked', async () => {
    const longInput = {
      Path: 'src/app.ts',
      Old_string: Array.from({ length: 20 }, (_, i) => `old-${i}`).join('\n'),
      New_string: Array.from({ length: 20 }, (_, i) => `new-${i}`).join('\n'),
    }
    const frame = makeFrame('completed', longInput)
    await act(async () => {
      root.render(<EditToolView frame={frame} />)
    })
    const expander = container.querySelector('.ai-tool-code-expander') as HTMLElement
    expect(expander).toBeTruthy()
    await act(async () => {
      expander.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onOpenFileMock).not.toHaveBeenCalled()
    // Expander still works: truncation notice disappears after expanding.
    expect(container.querySelector('.ai-tool-code-expander')!.textContent).toBe('collapse')
  })

  it('does not open the file while the edit is still running', async () => {
    const frame = makeFrame('running', editInput)
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <EditToolView frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    const diffArea = container.querySelector('.ai-edit-card-diff') as HTMLElement
    expect(diffArea.classList.contains('ai-edit-card-diff--openable')).toBe(false)
    await act(async () => {
      diffArea.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onOpenFileMock).not.toHaveBeenCalled()
  })
})
