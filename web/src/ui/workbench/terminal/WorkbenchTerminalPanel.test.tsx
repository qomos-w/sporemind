import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

import { I18nProvider } from '../../../i18n'
import { WorkbenchTerminalPanel } from './WorkbenchTerminalPanel'

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
    }
  },
}))

const xtermState = vi.hoisted(() => ({
  instances: [] as Array<{ writes: string[]; onDataHandler: null | ((data: string) => void) }>,
}))

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    writes: string[] = []
    onDataHandler: null | ((data: string) => void) = null
    constructor() {
      xtermState.instances.push(this as never)
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
    dispose() {}
  },
}))

vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

function renderPanel(props: { active?: boolean; projectRoot?: string; onActivity?: () => void } = {}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <WorkbenchTerminalPanel {...props} />
      </I18nProvider>,
    )
  })
  return { root, container }
}

beforeEach(() => {
  shellState.sessionOpen.mockReset().mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
  shellState.sessionFetch.mockReset().mockResolvedValue({ Chunks: [], NextIdx: 0, Truncated: false })
  shellState.sessionWrite.mockReset().mockResolvedValue({})
  shellState.sessionResize.mockReset().mockResolvedValue({})
  shellState.sessionClose.mockReset().mockResolvedValue({})
  shellState.outputHandler = null
  xtermState.instances.length = 0
})

describe('WorkbenchTerminalPanel', () => {
  it('renders the terminal skin and opens a real shell session', async () => {
    const { container } = renderPanel({ active: true, projectRoot: '/proj' })
    expect(container.querySelector('.wb-term')).not.toBeNull()
    expect(container.querySelector('.wb-term-body')).not.toBeNull()
    await act(async () => {})
    expect(shellState.sessionOpen).toHaveBeenCalledWith({}, { Cols: 80, Rows: 24, WorkingDirectory: '/proj' })
  })

  it('does not open a session while inactive', async () => {
    renderPanel({ active: false })
    await act(async () => {})
    expect(shellState.sessionOpen).not.toHaveBeenCalled()
  })

  it('reports output activity and writes keystrokes to the session', async () => {
    const onActivity = vi.fn()
    renderPanel({ active: true, onActivity })
    await act(async () => {})

    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'hello' })
    })
    expect(onActivity).toHaveBeenCalledTimes(1)

    act(() => {
      xtermState.instances[0]!.onDataHandler!('ls\r')
    })
    expect(shellState.sessionWrite).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Data: 'ls\r' })
  })

  it('shows an exit banner once the session exits', async () => {
    const { container } = renderPanel({ active: true })
    await act(async () => {})
    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'exit', Data: '', ExitCode: 0 })
    })
    expect(container.querySelector('.wb-term-status')).not.toBeNull()
  })
})
