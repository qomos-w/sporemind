import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ToolCallBlock } from './ToolCallBlock'
import { SmoothStreamContext } from '../../context/SmoothStreamContext'
import { DEFAULT_UI_EXPANSION_MODES } from '../../../../application/workspace-ui-state'
import type { ToolFrame } from '../../model/frame-types'

vi.mock('../../../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function makeBashFrame(status: ToolFrame['status']): ToolFrame {
  return {
    id: 'f-bash',
    type: 'tool',
    status,
    toolName: 'bash',
    input: JSON.stringify({ Command: 'npm', Args: ['test'] }),
    output: 'ok\n1 test passed',
    version: 1,
    exitCode: 0,
  } as unknown as ToolFrame
}

const waitFrames = (n: number) => new Promise<void>(resolve => {
  const step = () => { if (n-- > 0) requestAnimationFrame(step); else resolve() }
  requestAnimationFrame(step)
})

describe('run-command (bash) title-first unfold', () => {
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

  it('unfolds the output after the title delay when the frame arrives completed mid-stream', async () => {
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <ToolCallBlock frame={makeBashFrame('completed')} />
        </SmoothStreamContext.Provider>
      )
    })
    // Title-first: collapsed right after mount.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(false)

    await act(async () => { await new Promise(r => setTimeout(r, 380)) })
    await act(async () => { await waitFrames(4) })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
  })

  it('unfolds when running then completes', async () => {
    const frame = makeBashFrame('running')
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <ToolCallBlock frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(false)

    await act(async () => { await new Promise(r => setTimeout(r, 380)) })
    await act(async () => { await waitFrames(4) })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)

    await act(async () => {
      frame.status = 'completed'
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <ToolCallBlock frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    // auto-while-active: folds back after the collapse choreography, but the
    // expand was visible — open state survives until that fires.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
  })

  it('keeps the CSS title-first state when fold-expand streaming stops', async () => {
    const expansionModes = { ...DEFAULT_UI_EXPANSION_MODES, bash: 'fold-expand' as const }
    const frame = makeBashFrame('completed')
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes, expandDurationMs: 1000 }}>
          <ToolCallBlock frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    await act(async () => { await waitFrames(4) })
    // Body mounts immediately; CSS owns the title-first visual hold.
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
    expect(container.querySelector('.ai-collapsible')!.classList.contains('title-first')).toBe(true)

    // Turn stops streaming: the title-first choreography replays.
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: false, expansionModes, expandDurationMs: 1000 }}>
          <ToolCallBlock frame={frame} />
        </SmoothStreamContext.Provider>
      )
    })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(true)
    expect(container.querySelector('.ai-collapsible')!.classList.contains('title-first')).toBe(true)
  })

  it('completed frame mounts collapsed (no animation) when turnStreaming=false even during an active turn', async () => {
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={{ smoothStream: true, isStreaming: true, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }}>
          <ToolCallBlock frame={makeBashFrame('completed')} turnStreaming={false} />
        </SmoothStreamContext.Provider>
      )
    })
    const collapsible = container.querySelector('.ai-collapsible') as HTMLElement
    expect(collapsible).toBeTruthy()
    expect(collapsible.classList.contains('open')).toBe(false)
    await act(async () => { await new Promise(r => setTimeout(r, 500)) })
    expect(container.querySelector('.ai-collapsible')!.classList.contains('open')).toBe(false)
  })
})
