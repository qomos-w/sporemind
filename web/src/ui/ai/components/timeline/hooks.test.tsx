import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import {
  useTokenThroughput,
  estimateTokensFromText,
  useTimelineExpansionController,
  resetThroughputStoreForTests,
} from './hooks'
import type { TurnEnvelope } from '../../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const sleep = (ms: number) => new Promise<void>(r => setTimeout(r, ms))

function RateHarness({ envelope, isStreaming, paused }: {
  envelope: TurnEnvelope | undefined
  isStreaming: boolean
  paused?: boolean
}) {
  const { history, rate } = useTokenThroughput(envelope, isStreaming, paused)
  return (
    <div>
      <span data-testid="rate">{rate.toFixed(3)}</span>
      <span data-testid="len">{history.length}</span>
    </div>
  )
}

const textEnvelope = (content: string): TurnEnvelope => ({
  id: 't1',
  role: 'assistant',
  frames: [{ id: 'f1', type: 'text', content, status: 'running' }],
  timestamp: '1',
  completed: false,
  metadata: { turnId: 't1', turnState: 'running' },
})

describe('estimateTokensFromText', () => {
  it('counts CJK chars as ~1 token and latin as ~4 chars/token', () => {
    expect(estimateTokensFromText('')).toBe(0)
    expect(estimateTokensFromText('abcd')).toBe(1)
    expect(estimateTokensFromText('你好')).toBe(2)
    expect(estimateTokensFromText('a你')).toBeCloseTo(1.25, 5)
  })
})

describe('useTokenThroughput', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    resetThroughputStoreForTests()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const rateText = () => container.querySelector('[data-testid="rate"]')!.textContent
  const lenText = () => container.querySelector('[data-testid="len"]')!.textContent

  it('returns an empty history and 0 rate when not streaming', async () => {
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello world')} isStreaming={false} />)
    })
    expect(rateText()).toBe('0.000')
    expect(lenText()).toBe('0')
  })

  it('accumulates samples into the rolling history while content grows', async () => {
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hel')} isStreaming={true} />)
    })
    await act(async () => { await sleep(250) })
    expect(Number(lenText())).toBeGreaterThanOrEqual(1)

    // Grow content; the next sample reflects the streamed delta.
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello world and more streamed text')} isStreaming={true} />)
    })
    await act(async () => { await sleep(250) })
    expect(Number(lenText())).toBeGreaterThanOrEqual(2)
    expect(Number(rateText())).toBeGreaterThan(0)
  })

  it('freezes (does not clear) the history when streaming stops', async () => {
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello')} isStreaming={true} />)
    })
    await act(async () => { await sleep(250) })
    const frozenLen = Number(lenText())
    expect(frozenLen).toBeGreaterThanOrEqual(1)
    // Stop streaming — history must be retained (frozen), not reset to 0.
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello')} isStreaming={false} />)
    })
    expect(Number(lenText())).toBe(frozenLen)
  })

  it('resets history when the turn (envelope id) changes', async () => {
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello')} isStreaming={true} />)
    })
    await act(async () => { await sleep(250) })
    expect(Number(lenText())).toBeGreaterThanOrEqual(1)
    const other: TurnEnvelope = { ...textEnvelope('hi'), id: 't2' }
    await act(async () => {
      root.render(<RateHarness envelope={other} isStreaming={false} />)
    })
    expect(Number(lenText())).toBe(0)
  })

  it('freezes (does not drop to 0) while a tool call is running', async () => {
    // Mount short, then grow to establish a positive sampled rate.
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hel')} isStreaming={true} />)
    })
    await act(async () => { await sleep(250) })
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello streaming tokens here')} isStreaming={true} />)
    })
    await act(async () => { await sleep(260) })
    const activeRate = Number(rateText())
    expect(activeRate).toBeGreaterThan(0)

    // A tool call starts (running tool frame). No new text grows, but the
    // timeline must hold the last value rather than collapsing to 0.
    const envWithTool: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [
        { id: 'f1', type: 'text', content: 'hello streaming tokens here', status: 'completed' },
        { id: 'tool1', type: 'tool', toolName: 'shell', input: '{}', status: 'running' },
      ],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await act(async () => {
      root.render(<RateHarness envelope={envWithTool} isStreaming={true} />)
    })
    await act(async () => { await sleep(260) })
    // The frozen rate must stay well above 0 (held at the pre-tool value).
    expect(Number(rateText())).toBeGreaterThan(activeRate * 0.5)
  })

  it('keeps the timeline across unmount/remount (agent switch) instead of restarting', async () => {
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hel')} isStreaming={true} />)
    })
    await act(async () => { await sleep(250) })
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello world, tokens streaming along')} isStreaming={true} />)
    })
    await act(async () => { await sleep(260) })
    const lenBefore = Number(lenText())
    const rateBefore = Number(rateText())
    expect(lenBefore).toBeGreaterThanOrEqual(2)
    expect(rateBefore).toBeGreaterThan(0)

    // Switch away (unmount) — the turn keeps streaming in the background.
    await act(async () => { root.unmount() })
    // Switch back: the envelope grew while away. The sampled history is
    // preserved and the away growth is not re-counted as a one-tick burst.
    const container2 = document.createElement('div')
    document.body.appendChild(container2)
    const root2 = createRoot(container2)
    const envAway = textEnvelope('hello world, tokens streaming along and even more while away')
    const rate2Text = () => container2.querySelector('[data-testid="rate"]')!.textContent
    const len2Text = () => container2.querySelector('[data-testid="len"]')!.textContent
    try {
      await act(async () => {
        root2.render(<RateHarness envelope={envAway} isStreaming={true} />)
      })
      expect(Number(len2Text())).toBeGreaterThanOrEqual(lenBefore)
      await act(async () => { await sleep(250) })
      // No burst from the away growth: rate stays in the same ballpark.
      expect(Number(rate2Text())).toBeLessThan(rateBefore * 3)

      // Streaming continues after remount: fresh growth produces fresh samples.
      await act(async () => {
        root2.render(<RateHarness envelope={textEnvelope('hello world, tokens streaming along and even more while away plus fresh deltas')} isStreaming={true} />)
      })
      await act(async () => { await sleep(260) })
      expect(Number(rate2Text())).toBeGreaterThan(0)
      expect(Number(len2Text())).toBeGreaterThanOrEqual(lenBefore)
    } finally {
      await act(async () => { root2.unmount() })
      container2.remove()
    }
  })

  it('counts tool-call argument tokens spread over the silent window, not tool results', async () => {
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hel')} isStreaming={true} />)
    })
    await act(async () => { await sleep(250) })
    await act(async () => {
      root.render(<RateHarness envelope={textEnvelope('hello world streamed')} isStreaming={true} />)
    })
    // Silent window while the model generates tool arguments (nothing visible).
    await act(async () => { await sleep(500) })
    expect(Number(rateText())).toBe(0)

    // The tool_use block arrives at once (~95 tokens of arguments) together
    // with a large result. Arguments must count — spread over the silent
    // window, not a single-tick spike — and the result must not count.
    const envWithTool: TurnEnvelope = {
      id: 't1',
      role: 'assistant',
      frames: [
        { id: 'f1', type: 'text', content: 'hello world streamed', status: 'completed' },
        {
          id: 'tool1',
          type: 'tool',
          toolName: 'search',
          input: JSON.stringify({ path: '/tmp', q: 'x'.repeat(360) }),
          output: 'y'.repeat(4000),
          status: 'running',
        },
      ],
      timestamp: '1',
      completed: false,
      metadata: { turnId: 't1', turnState: 'running' },
    }
    await act(async () => {
      root.render(<RateHarness envelope={envWithTool} isStreaming={true} />)
    })
    const spreadRate = Number(rateText())
    // ~95 tokens over a sub-second window: clearly nonzero, far from the
    // ~1000+ tok/s a naive single-tick dump (or counted result) would show.
    expect(spreadRate).toBeGreaterThan(5)
    expect(spreadRate).toBeLessThan(400)
  })
})

describe('useTimelineExpansionController', () => {
  it('seeds iconPhase to idle for a completed history-loaded step', () => {
    const { result } = renderHook(() =>
      useTimelineExpansionController({
        status: 'completed',
        expandable: true,
        expansionMode: 'manual',
        turnStreaming: false,
      }),
    )
    expect(result.current.iconPhase).toBe('idle')
    expect(result.current.historyLoaded).toBe(true)
  })

  it('seeds iconPhase to spinning for an active running step', () => {
    const { result } = renderHook(() =>
      useTimelineExpansionController({
        status: 'running',
        expandable: true,
        expansionMode: 'manual',
        turnStreaming: true,
      }),
    )
    expect(result.current.iconPhase).toBe('spinning')
    expect(result.current.historyLoaded).toBe(false)
  })

  it('keeps a completed step idle even while turnStreaming is transiently true', () => {
    const { result } = renderHook(() =>
      useTimelineExpansionController({
        status: 'completed',
        expandable: true,
        expansionMode: 'manual',
        turnStreaming: true,
      }),
    )
    expect(result.current.iconPhase).toBe('idle')
    expect(result.current.historyLoaded).toBe(false)
  })

  describe('completed frame mounting mid-stream (auto-while-active)', () => {
    let container: HTMLDivElement
    let root: Root
    let state: { iconPhase: string; isOpen: boolean; isActive: boolean }

    function ControllerHarness() {
      state = useTimelineExpansionController({
        status: 'completed',
        expandable: true,
        expansionMode: 'auto-while-active',
        turnStreaming: true,
      })
      return null
    }

    beforeEach(() => {
      container = document.createElement('div')
      document.body.appendChild(container)
      root = createRoot(container)
    })

    afterEach(() => {
      act(() => root.unmount())
      container.remove()
    })

    it('runs the full icon choreography instead of stranding iconPhase in exiting', async () => {
      await act(async () => {
        root.render(<ControllerHarness />)
      })
      // The mount effect arms: exiting -> arrow -> entering -> idle.
      expect(state.iconPhase).toBe('exiting')
      await act(async () => { await sleep(300) })
      expect(state.iconPhase).toBe('arrow')
      await act(async () => { await sleep(500) })
      expect(state.iconPhase).toBe('entering')
      await act(async () => { await sleep(300) })
      expect(state.iconPhase).toBe('idle')
      // Auto-collapse lands after the unfold completes.
      await act(async () => { await sleep(2200) })
      expect(state.isOpen).toBe(false)
    })
  })
})
