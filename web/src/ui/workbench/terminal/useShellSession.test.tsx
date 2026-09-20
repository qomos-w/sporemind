import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

import { useShellSession, type UseShellSessionResult } from './useShellSession'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as any).ResizeObserver = ResizeObserverStub

vi.mock('../../../application/generated-client', () => ({ client: {} }))

interface OutputEvent {
  SessionId: string
  Idx: number
  Kind: string
  Data: string
  ExitCode?: number
}

const shellState = vi.hoisted(() => ({
  sessionOpen: vi.fn(),
  sessionFetch: vi.fn(),
  sessionWrite: vi.fn(),
  sessionResize: vi.fn(),
  sessionClose: vi.fn(),
  unsubscribe: vi.fn(),
  outputHandler: null as null | ((ev: OutputEvent) => void),
}))

vi.mock('../../../gen-clients/shell/client', () => ({
  sessionOpen: (...a: unknown[]) => shellState.sessionOpen(...a),
  sessionFetch: (...a: unknown[]) => shellState.sessionFetch(...a),
  sessionWrite: (...a: unknown[]) => shellState.sessionWrite(...a),
  sessionResize: (...a: unknown[]) => shellState.sessionResize(...a),
  sessionClose: (...a: unknown[]) => shellState.sessionClose(...a),
  OnShellSessionOutput: (_c: unknown, h: (ev: unknown) => void) => {
    shellState.outputHandler = h as never
    return () => {
      shellState.outputHandler = null
      shellState.unsubscribe()
    }
  },
}))

const xtermState = vi.hoisted(() => ({
  instances: [] as XtermStub[],
}))

interface XtermStub {
  cols: number
  rows: number
  writes: string[]
  onDataHandler: null | ((data: string) => void)
  disposed: boolean
  loadAddon(): void
  open(): void
  onData(h: (data: string) => void): { dispose(): void }
  write(d: string): void
  focus(): void
  clear(): void
  dispose(): void
}

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    writes: string[] = []
    onDataHandler: null | ((data: string) => void) = null
    disposed = false
    constructor() {
      xtermState.instances.push(this as unknown as XtermStub)
    }
    loadAddon() {}
    open() {}
    onData(h: (data: string) => void) {
      this.onDataHandler = h
      return { dispose: () => { this.onDataHandler = null } }
    }
    write(d: string) {
      this.writes.push(String(d))
    }
    focus() {}
    clear() {}
    dispose() {
      this.disposed = true
    }
  },
}))

vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

let last: UseShellSessionResult | null = null

function Harness({ enabled, projectRoot, onOutput }: { enabled?: boolean; projectRoot?: string; onOutput?: (ev: OutputEvent) => void }) {
  const session = useShellSession({ enabled, projectRoot, onOutput })
  last = session
  return <div ref={session.containerRef} data-testid="term" data-phase={session.state.phase} />
}

function renderHarness(props: { enabled?: boolean; projectRoot?: string; onOutput?: (ev: OutputEvent) => void } = {}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(<Harness {...props} />)
  })
  return { root, container }
}

beforeEach(() => {
  shellState.sessionOpen.mockReset().mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
  shellState.sessionFetch.mockReset().mockResolvedValue({ Chunks: [], NextIdx: 0, Truncated: false })
  shellState.sessionWrite.mockReset().mockResolvedValue({})
  shellState.sessionResize.mockReset().mockResolvedValue({})
  shellState.sessionClose.mockReset().mockResolvedValue({})
  shellState.unsubscribe.mockReset()
  shellState.outputHandler = null
  xtermState.instances.length = 0
  last = null
})

describe('useShellSession', () => {
  it('subscribes before opening and replays fetch(0) with working directory', async () => {
    renderHarness({ projectRoot: '/proj' })

    // Subscription is established synchronously, before the async open.
    expect(shellState.outputHandler).toBeTypeOf('function')
    await act(async () => {})

    expect(shellState.sessionOpen).toHaveBeenCalledWith({}, { Cols: 80, Rows: 24, WorkingDirectory: '/proj' })
    expect(shellState.sessionFetch).toHaveBeenCalledWith({}, { SessionId: 'sess-1', FromIdx: 0 })
    expect(shellState.sessionResize).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Cols: 80, Rows: 24 })
    expect(last?.state.phase).toBe('ready')
    expect(last?.sessionId).toBe('sess-1')
  })

  it('applies replayed chunks idempotently by Idx and streams live output', async () => {
    shellState.sessionFetch.mockResolvedValue({
      Chunks: [
        { SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'replay-0' },
        { SessionId: 'sess-1', Idx: 2, Kind: 'stdout', Data: 'replay-2' },
      ],
      NextIdx: 3,
      Truncated: false,
    })
    const onOutput = vi.fn()
    renderHarness({ onOutput })
    await act(async () => {})
    const term = xtermState.instances[0]!

    expect(term.writes).toContain('replay-0')
    expect(term.writes).toContain('replay-2')
    expect(onOutput).toHaveBeenCalledTimes(2)

    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'duplicate' })
    })
    expect(term.writes).not.toContain('duplicate')

    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 3, Kind: 'stdout', Data: 'live' })
    })
    expect(term.writes).toContain('live')
    expect(onOutput).toHaveBeenCalledTimes(3)
  })

  it('marks the session exited on an exit chunk', async () => {
    renderHarness()
    await act(async () => {})
    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'exit', Data: 'bye', ExitCode: 2 })
    })
    expect(last?.state.phase).toBe('exited')
    expect(last?.state.exitCode).toBe(2)
  })

  it('forwards send() to session_write once the session is ready', async () => {
    renderHarness()
    await act(async () => {})
    act(() => {
      last?.send('ls\r')
    })
    expect(shellState.sessionWrite).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Data: 'ls\r' })
  })

  it('unsubscribes and closes the session on unmount', async () => {
    const { root } = renderHarness()
    await act(async () => {})
    act(() => {
      root.unmount()
    })
    expect(shellState.unsubscribe).toHaveBeenCalledTimes(1)
    expect(shellState.sessionClose).toHaveBeenCalledWith({}, { SessionId: 'sess-1' })
    expect(xtermState.instances[0]!.disposed).toBe(true)
  })

  it('updates phase to error when the session fails to open', async () => {
    shellState.sessionOpen.mockRejectedValue(new Error('boom'))
    renderHarness()
    await act(async () => {})
    expect(last?.state.phase).toBe('error')
    expect(last?.state.error).toBe('boom')
  })
})
