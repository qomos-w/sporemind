import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import type { ReactNode } from 'react'
import { useGlassDebug, GLASS_DEBUG_TIMELINE_MAX } from './useGlassDebug'
import type { GlassDebugState, GlassRenderEvent, GlassLifecycleEvent, GlassSpeakEvent } from '../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const { eventHandlers, makeCapture } = vi.hoisted(() => {
  const eventHandlers: Record<string, (payload: unknown) => void> = {}
  return {
    eventHandlers,
    makeCapture: (name: string) => (_client: unknown, handler: (payload: unknown) => void) => {
      eventHandlers[name] = handler
      return () => {}
    },
  }
})

vi.mock('../../application/generated-client', () => ({ client: {} }))

const stateMock = vi.fn()
vi.mock('../../gen-clients/glass_interact/client', () => ({
  debugState: (...args: unknown[]) => stateMock(...args),
}))

vi.mock('../../gen-clients/glassinteract/client', () => ({
  OnGlassOnline: makeCapture('glass.online'),
  OnGlassOffline: makeCapture('glass.offline'),
  OnGlassReconnected: makeCapture('glass.reconnected'),
  OnGlassReplaced: makeCapture('glass.replaced'),
  OnGlassRender: makeCapture('glass.render'),
  OnGlassSpeak: makeCapture('glass.speak'),
  OnGlassTranscript: makeCapture('glass.transcript'),
}))

function baseState(over: Partial<GlassDebugState> = {}): GlassDebugState {
  return {
    Session: {
      SessionId: 'sess-1',
      DeviceId: 'dev-1',
      Generation: 2,
      Online: true,
      LastSeen: '2026-08-01T10:00:00Z',
      Capabilities: [{ Name: 'display', Version: '1.0', Features: ['text'] }],
    },
    Capabilities: [{ Name: 'display', Version: '1.0', Features: ['text'] }],
    CurrentFrame: { Text: 'Hello glass', Layout: 'single' },
    AudioAck: { SessionId: 'sess-1', UtteranceId: 'u1', HighestContiguousSeq: 4, EndReceived: false, Accepted: true },
    Stats: { Utterances: 1, Transcripts: 0, Renders: 1, Speaks: 1, SpeakDeduped: 0, Errors: 0 },
    Timeline: [{ Kind: 'render', Detail: 'text=Hello glass', Timestamp: '2026-08-01T10:00:01Z' }],
    Deliveries: [],
    ...over,
  }
}

function renderProbe(autoRefresh = false) {
  let captured: ReturnType<typeof useGlassDebug> | null = null
  function Probe(): ReactNode {
    captured = useGlassDebug(autoRefresh)
    return null
  }
  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => {
    root.render(<Probe />)
  })
  return { root, get: () => captured! }
}

describe('useGlassDebug', () => {
  beforeEach(() => {
    for (const key of Object.keys(eventHandlers)) delete eventHandlers[key]
    stateMock.mockReset()
    stateMock.mockResolvedValue({ State: baseState() })
  })

  it('fetches the debug snapshot on mount without a target', async () => {
    const probe = renderProbe()
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    expect(stateMock).toHaveBeenCalledTimes(1)
    expect(stateMock.mock.calls[0]![1]).toEqual({})
    expect(stateMock.mock.calls[0]![2]).toBeUndefined()
    const r = probe.get()
    expect(r.loading).toBe(false)
    expect(r.error).toBeNull()
    expect(r.state?.Session?.SessionId).toBe('sess-1')
    expect(r.state?.CurrentFrame?.Text).toBe('Hello glass')
    expect(r.state?.Timeline.length).toBe(1)

    act(() => {
      probe.root.unmount()
    })
  })

  it('registers all seven glass event subscriptions', async () => {
    const probe = renderProbe()
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    expect(Object.keys(eventHandlers).sort()).toEqual([
      'glass.offline',
      'glass.online',
      'glass.reconnected',
      'glass.render',
      'glass.replaced',
      'glass.speak',
      'glass.transcript',
    ])
    act(() => {
      probe.root.unmount()
    })
  })

  it('applies glass.render events to the current frame and timeline', async () => {
    const probe = renderProbe()
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    const ev: GlassRenderEvent = {
      SessionId: 'sess-1',
      Generation: 2,
      Frame: { Text: 'Updated frame', Layout: 'list' },
      Timestamp: '2026-08-01T10:00:02Z',
    }
    act(() => {
      eventHandlers['glass.render']!(ev)
    })

    const r = probe.get()
    expect(r.state?.CurrentFrame?.Text).toBe('Updated frame')
    expect(r.state?.Timeline[0]).toMatchObject({ Kind: 'render', Detail: 'text=Updated frame layout=list' })

    act(() => {
      probe.root.unmount()
    })
  })

  it('applies glass.offline to the session connection state', async () => {
    const probe = renderProbe()
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    const ev: GlassLifecycleEvent = {
      Kind: 'offline',
      SessionId: 'sess-1',
      DeviceId: 'dev-1',
      Generation: 2,
      Reason: 'reconnect grace elapsed',
      Timestamp: '2026-08-01T10:05:00Z',
    }
    act(() => {
      eventHandlers['glass.offline']!(ev)
    })

    const r = probe.get()
    expect(r.state?.Session?.Online).toBe(false)
    expect(r.state?.Session?.OfflineAt).toBe('2026-08-01T10:05:00Z')
    expect(r.connectionEvents[0]).toMatchObject({ Kind: 'offline' })

    act(() => {
      probe.root.unmount()
    })
  })

  it('marks duplicate speaks as speak_deduped on the timeline', async () => {
    const probe = renderProbe()
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })

    const ev: GlassSpeakEvent = {
      SessionId: 'sess-1',
      Generation: 2,
      MessageId: 'msg-9',
      Text: 'hi',
      Duplicate: true,
      Timestamp: '2026-08-01T10:00:03Z',
    }
    act(() => {
      eventHandlers['glass.speak']!(ev)
    })

    expect(probe.get().state?.Timeline[0]).toMatchObject({ Kind: 'speak_deduped', Detail: 'msg-9' })

    act(() => {
      probe.root.unmount()
    })
  })

  it('caps the local timeline at the server bound', async () => {
    stateMock.mockResolvedValue({ State: baseState({ Timeline: [] }) })
    const probe = renderProbe()
    await act(async () => {
      await new Promise(r => setTimeout(r, 10))
    })
    for (let i = 0; i < GLASS_DEBUG_TIMELINE_MAX + 10; i++) {
      const ev: GlassRenderEvent = {
        SessionId: 'sess-1',
        Generation: 2,
        Frame: { Text: `frame-${i}` },
        Timestamp: '2026-08-01T10:00:00Z',
      }
      act(() => {
        eventHandlers['glass.render']!(ev)
      })
    }
    expect(probe.get().state?.Timeline.length).toBe(GLASS_DEBUG_TIMELINE_MAX)
    expect(probe.get().state?.Timeline[0]).toMatchObject({ Detail: 'text=frame-41' })

    act(() => {
      probe.root.unmount()
    })
  })
})
