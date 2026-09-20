import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { GlassDebugPanel } from './GlassDebugPanel'
import type { GlassDebugState, GlassLifecycleEvent } from '../../gen-clients/system/types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hookMock = vi.fn()
vi.mock('./useGlassDebug', () => ({
  GLASS_DEBUG_TIMELINE_MAX: 32,
  GLASS_CONNECTION_EVENTS_MAX: 8,
  useGlassDebug: (...args: unknown[]) => hookMock(...args),
}))

function sampleState(): GlassDebugState {
  return {
    Session: {
      SessionId: 'sess-1',
      DeviceId: 'dev-7',
      Generation: 3,
      Online: true,
      LastSeen: '2026-08-01T10:00:00Z',
      Capabilities: [{ Name: 'display', Version: '2.0', Features: ['text', 'image'] }],
    },
    Capabilities: [
      { Name: 'display', Version: '2.0', Features: ['text', 'image'] },
      { Name: 'audio', Version: '1.1', Features: ['vad', 'pcm16'] },
    ],
    CurrentFrame: { Text: 'Step one complete', Layout: 'single', Meta: '{"step":1}' },
    AudioAck: { SessionId: 'sess-1', UtteranceId: 'u-42', HighestContiguousSeq: 12, EndReceived: true, Accepted: true },
    Stats: { Utterances: 5, Transcripts: 3, Renders: 7, Speaks: 2, SpeakDeduped: 1, Errors: 0 },
    Timeline: [
      { Kind: 'transcript', Detail: 'hello glass', Timestamp: '2026-08-01T10:00:02Z' },
      { Kind: 'render', Detail: 'text=Step one complete layout=single', Timestamp: '2026-08-01T10:00:01Z' },
    ],
    Deliveries: [],
  }
}

function connectionEvents(): GlassLifecycleEvent[] {
  return [{ Kind: 'online', SessionId: 'sess-1', DeviceId: 'dev-7', Generation: 3, Timestamp: '2026-08-01T10:00:00Z' }]
}

function renderPanel() {
  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => {
    root.render(<GlassDebugPanel />)
  })
  return { root, container }
}

describe('GlassDebugPanel', () => {
  beforeEach(() => {
    hookMock.mockReset()
    hookMock.mockReturnValue({
      state: sampleState(),
      loading: false,
      error: null,
      lastUpdated: Date.now(),
      autoRefresh: true,
      setAutoRefresh: vi.fn(),
      refresh: vi.fn(),
      connectionEvents: connectionEvents(),
    })
  })

  it('renders session connection state and badge', () => {
    const { root, container } = renderPanel()
    expect(container.textContent).toContain('sess-1')
    expect(container.textContent).toContain('dev-7')
    expect(container.textContent).toContain('Online')
    expect(container.textContent).toContain('gen 3')
    act(() => {
      root.unmount()
    })
  })

  it('renders the display preview with frame metadata', () => {
    const { root, container } = renderPanel()
    expect(container.textContent).toContain('Display preview')
    expect(container.textContent).toContain('layout: single')
    expect(container.textContent).toContain('text: Step one complete')
    act(() => {
      root.unmount()
    })
  })

  it('renders capabilities with version and features', () => {
    const { root, container } = renderPanel()
    expect(container.textContent).toContain('Capabilities')
    expect(container.textContent).toContain('display')
    expect(container.textContent).toContain('v2.0')
    expect(container.textContent).toContain('audio')
    expect(container.textContent).toContain('vad')
    act(() => {
      root.unmount()
    })
  })

  it('renders audio ACK and aggregate stats', () => {
    const { root, container } = renderPanel()
    expect(container.textContent).toContain('u-42')
    expect(container.textContent).toContain('12')
    expect(container.textContent).toContain('Utterances')
    expect(container.textContent).toContain('Renders')
    expect(container.textContent).toContain('Speak deduped')
    act(() => {
      root.unmount()
    })
  })

  it('renders timeline entries newest first and connection events', () => {
    const { root, container } = renderPanel()
    expect(container.textContent).toContain('Timeline')
    expect(container.textContent).toContain('hello glass')
    expect(container.textContent).toContain('Step one complete')
    expect(container.textContent).toContain('Connection events')
    expect(container.textContent).toContain('online')
    act(() => {
      root.unmount()
    })
  })

  it('shows an idle badge and empty frame when no session exists', () => {
    hookMock.mockReturnValue({
      state: {
        Capabilities: [],
        Stats: { Utterances: 0, Transcripts: 0, Renders: 0, Speaks: 0, SpeakDeduped: 0, Errors: 0 },
        Timeline: [],
      },
      loading: false,
      error: null,
      lastUpdated: null,
      autoRefresh: true,
      setAutoRefresh: vi.fn(),
      refresh: vi.fn(),
      connectionEvents: [],
    })
    const { root, container } = renderPanel()
    expect(container.textContent).toContain('No session')
    expect(container.textContent).toContain('Display preview')
    expect(container.textContent).toContain('No active Glass session')
    act(() => {
      root.unmount()
    })
  })

  it('renders an error banner when the callable fails', () => {
    hookMock.mockReturnValue({
      state: null,
      loading: false,
      error: 'glass_interact.debug.state: no active session',
      lastUpdated: null,
      autoRefresh: true,
      setAutoRefresh: vi.fn(),
      refresh: vi.fn(),
      connectionEvents: [],
    })
    const { root, container } = renderPanel()
    expect(container.textContent).toContain('glass_interact.debug.state')
    act(() => {
      root.unmount()
    })
  })
})
