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

let container: HTMLElement
let root: Root

function makeFrame(status: ToolFrame['status']): ToolFrame {
  return {
    id: 'f-shell',
    type: 'tool',
    status,
    toolName: 'shell_exec',
    input: JSON.stringify({ Command: 'go', Args: ['build', './...'] }),
    output: 'ok',
    version: 1,
  } as ToolFrame
}

const ctx = (streaming: boolean) => (
  { smoothStream: true, isStreaming: streaming, expansionModes: DEFAULT_UI_EXPANSION_MODES, expandDurationMs: 1000 }
)

const wait = (ms: number) => new Promise(r => setTimeout(r, ms))

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.restoreAllMocks()
})

describe('runcommand live lifecycle (pending -> running -> completed)', () => {
  it('stays title-collapsed for the first 320ms, then unfolds', async () => {
    const frame = makeFrame('pending')
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={ctx(true) as never}>
          <ToolCallBlock frame={frame} connectorMode="none" />
        </SmoothStreamContext.Provider>
      )
    })
    // pending: title row renders, body region exists but stays closed so the
    // status->running transition doesn't remount the collapsible (remount
    // resets expansion state and kills the unfold choreography).
    let collapsible = container.querySelector('.ai-collapsible')
    expect(collapsible).toBeTruthy()
    expect(collapsible!.classList.contains('open')).toBe(false)

    await act(async () => {
      frame.status = 'running'
      root.render(
        <SmoothStreamContext.Provider value={ctx(true) as never}>
          <ToolCallBlock frame={frame} connectorMode="none" />
        </SmoothStreamContext.Provider>
      )
    })
    // Inside the 320ms title window: still collapsed
    collapsible = container.querySelector('.ai-collapsible') as HTMLElement
    expect(collapsible).toBeTruthy()
    expect(collapsible.classList.contains('open')).toBe(false)

    await act(async () => { await wait(400) })
    await act(async () => {
      await new Promise<void>(resolve => {
        const step = () => { requestAnimationFrame(step2); }
        const step2 = () => { requestAnimationFrame(() => resolve()) }
        requestAnimationFrame(step)
      })
    })
    collapsible = container.querySelector('.ai-collapsible') as HTMLElement
    expect(collapsible.classList.contains('open')).toBe(true)
    expect(container.textContent).toContain('go build ./...')
  })

  it('fast command completing inside the title window still unfolds visibly', async () => {
    const frame = makeFrame('running')
    await act(async () => {
      root.render(
        <SmoothStreamContext.Provider value={ctx(true) as never}>
          <ToolCallBlock frame={frame} connectorMode="none" />
        </SmoothStreamContext.Provider>
      )
    })
    // completes 100ms in, before the 320ms title delay fires
    await act(async () => { await wait(100) })
    await act(async () => {
      frame.status = 'completed'
      root.render(
        <SmoothStreamContext.Provider value={ctx(true) as never}>
          <ToolCallBlock frame={frame} connectorMode="none" />
        </SmoothStreamContext.Provider>
      )
    })
    // Completion chains the collapse off the title timer; the body unfolds
    // and stays open for the full wait window (500 + 1000ms past the chain
    // start), well beyond the 1s CSS transition.
    await act(async () => { await wait(700) })
    const collapsible = container.querySelector('.ai-collapsible') as HTMLElement
    expect(collapsible.classList.contains('open')).toBe(true)
    expect(container.textContent).toContain('go build ./...')
  })
})
