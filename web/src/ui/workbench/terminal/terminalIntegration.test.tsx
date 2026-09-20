import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

import { I18nProvider } from '../../../i18n'
import { WorkbenchTerminalPanel } from './WorkbenchTerminalPanel'
import { useTerminalPresence, type TerminalPresenceController } from './useTerminalPresence'

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

let controller: TerminalPresenceController | null = null

/**
 * Minimal host that mirrors how the workbench surface drives the terminal card:
 * the card is mounted only while the presence controller says it is summoned,
 * and each shell output chunk feeds the controller to reset the idle clock.
 */
function Harness() {
  const presence = useTerminalPresence({ idleMs: 1000, tickMs: 250 })
  controller = presence
  return (
    <div data-testid="surface">
      {!presence.hidden && <WorkbenchTerminalPanel active onActivity={presence.noteOutput} />}
    </div>
  )
}

function renderHarness() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <Harness />
      </I18nProvider>,
    )
  })
  return { root, container }
}

beforeEach(() => {
  vi.useFakeTimers()
  shellState.sessionOpen.mockReset().mockResolvedValue({ SessionId: 'sess-1', Mode: 'pty' })
  shellState.sessionFetch.mockReset().mockResolvedValue({ Chunks: [], NextIdx: 0, Truncated: false })
  shellState.sessionWrite.mockReset().mockResolvedValue({})
  shellState.sessionResize.mockReset().mockResolvedValue({})
  shellState.sessionClose.mockReset().mockResolvedValue({})
  shellState.outputHandler = null
  xtermState.instances.length = 0
  controller = null
})

afterEach(() => {
  vi.useRealTimers()
})

describe('workbench terminal card (summon → run → retreat)', () => {
  it('stays hidden until a keyword mention, then runs a real command with scrolling output', async () => {
    const { container } = renderHarness()
    expect(container.querySelector('.wb-term')).toBeNull()

    // Agent text mentions the build → keyword summon.
    act(() => controller!.noteText('please run the build', 'agent'))
    expect(container.querySelector('.wb-term')).not.toBeNull()

    await act(async () => {})
    expect(shellState.sessionOpen).toHaveBeenCalledTimes(1)

    // Output chunks stream into the scrollback (rolling output).
    act(() => {
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 0, Kind: 'stdout', Data: 'building...\n' })
      shellState.outputHandler!({ SessionId: 'sess-1', Idx: 1, Kind: 'stdout', Data: 'done\n' })
    })
    const term = xtermState.instances[0]!
    expect(term.writes).toContain('building...\n')
    expect(term.writes).toContain('done\n')

    // A typed command is written to the live session.
    act(() => term.onDataHandler!('make build-desktop\r'))
    expect(shellState.sessionWrite).toHaveBeenCalledWith({}, { SessionId: 'sess-1', Data: 'make build-desktop\r' })
  })

  it('auto-retreats once output goes idle', async () => {
    const { container } = renderHarness()
    act(() => controller!.noteText('show me the terminal', 'user'))
    expect(container.querySelector('.wb-term')).not.toBeNull()
    await act(async () => {})

    // No further output for longer than the idle window → tick retreats the card.
    act(() => {
      vi.advanceTimersByTime(1500)
    })
    expect(controller!.hidden).toBe(true)
    expect(container.querySelector('.wb-term')).toBeNull()
    expect(shellState.sessionClose).toHaveBeenCalled()
  })
})
